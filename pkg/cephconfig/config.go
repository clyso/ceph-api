package cephconfig

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/rs/zerolog"
)

//go:embed config-index.json
var configIndexFile embed.FS

// ConfigParamInfo represents the help data for a Ceph configuration parameter
type ConfigParamInfo struct {
	Name               string      `json:"name"`
	Type               string      `json:"type"`
	Level              string      `json:"level"`
	Desc               string      `json:"desc"`
	LongDesc           string      `json:"long_desc"`
	Default            interface{} `json:"default"`
	DaemonDefault      interface{} `json:"daemon_default"`
	Tags               []string    `json:"tags"`
	Services           []string    `json:"services"`
	SeeAlso            []string    `json:"see_also"`
	EnumValues         []string    `json:"enum_values"`
	Min                *MinMax     `json:"min"`
	Max                *MinMax     `json:"max"`
	CanUpdateAtRuntime bool        `json:"can_update_at_runtime"`
	Flags              []string    `json:"flags"`
}

// MinMax is a float64 that can be unmarshaled from a JSON string or number
// If the value is not present or not a valid number, the pointer will be nil
type MinMax float64

func (m *MinMax) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	// Try to unmarshal as number
	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		*m = MinMax(num)
		return nil
	}
	// Try to unmarshal as string, then parse as float
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		if str == "" {
			return nil
		}
		parsed, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return err
		}
		*m = MinMax(parsed)
		return nil
	}
	return nil // ignore if not a number or string
}

// QueryParams contains the parameters for config search
type QueryParams struct {
	Service  pb.SearchConfigRequest_ServiceType
	Name     string
	FullText string
	Level    pb.SearchConfigRequest_ConfigLevel
	Sort     pb.SearchConfigRequest_SortField
	Order    pb.SearchConfigRequest_SortOrder
}

// ConfigParams is a map of parameter names to their information
type ConfigParams map[string]ConfigParamInfo

// Config manages Ceph configuration parameters
type Config struct {
	params ConfigParams
}

// loadConfigParams loads all Ceph configuration parameters from the embedded JSON file
func loadConfigParams(ctx context.Context) (ConfigParams, error) {
	// Open the embedded config index file
	logger := zerolog.Ctx(ctx)
	logger.Info().Msg("Loading Ceph configuration parameters from embedded JSON file")

	// Read the file data
	data, err := configIndexFile.ReadFile("config-index.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded config-index.json: %w", err)
	}

	// Parse the JSON structure (array of ConfigParamInfo objects)
	var jsonArray []ConfigParamInfo
	err = json.Unmarshal(data, &jsonArray)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config index JSON: %w", err)
	}

	// Convert to our ConfigParams map format
	configParams := make(ConfigParams)
	for _, info := range jsonArray {
		configParams[info.Name] = info
	}

	return configParams, nil
}

// NewConfig creates a new Config instance and updates parameters from the cluster synchronously
func NewConfig(ctx context.Context, radosSvc *rados.Svc, skipUpdate bool) (*Config, error) {
	params, err := loadConfigParams(ctx)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		params: params,
	}

	if skipUpdate {
		return cfg, nil
	}

	logger := zerolog.Ctx(ctx)
	logger.Info().Msg("Updating Ceph configuration parameters from cluster")

	// Execute 'ceph config ls' command to get current configuration
	const monCmd = `{"prefix": "config ls", "format": "json"}`
	cmdRes, err := radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		logger.Err(err).Msg("Failed to execute 'config ls' command")
		return cfg, nil // Return with just embedded params
	}

	// Parse the result - it's an array of parameter names
	var clusterParams []string
	err = json.Unmarshal(cmdRes, &clusterParams)
	if err != nil {
		logger.Err(err).Msg("Failed to unmarshal config ls response")
		return cfg, nil
	}

	// Create a map for quick lookup of parameter names
	clusterParamsMap := make(map[string]bool)
	for _, param := range clusterParams {
		clusterParamsMap[param] = true
	}

	// Get the current parameter names
	currentParamsMap := make(map[string]bool)
	for name := range cfg.params {
		currentParamsMap[name] = true
	}

	// Find parameters to add (present in cluster but not in our map)
	var paramsToAdd []string
	for name := range clusterParamsMap {
		if !currentParamsMap[name] {
			paramsToAdd = append(paramsToAdd, name)
		}
	}

	// Find parameters to remove (present in our map but not in cluster)
	var paramsToRemove []string
	for name := range currentParamsMap {
		if !clusterParamsMap[name] {
			paramsToRemove = append(paramsToRemove, name)
		}
	}

	// Add new parameters with minimal info (they will need full info from 'config help' later)
	for _, name := range paramsToAdd {
		cfg.params[name] = ConfigParamInfo{
			Name:     name,
			Level:    "",
			Type:     "",
			Services: []string{""},
		}
	}

	// Remove parameters that don't exist in the cluster
	for _, name := range paramsToRemove {
		delete(cfg.params, name)
	}

	// Populate details for new parameters
	if len(paramsToAdd) > 0 {
		cfg.populateParamsDetails(ctx, radosSvc, paramsToAdd, logger)
	}

	logger.Info().
		Int("total_params", len(cfg.params)).
		Int("added", len(paramsToAdd)).
		Int("removed", len(paramsToRemove)).
		Msg("Updated Ceph configuration parameters from cluster")

	return cfg, nil
}

// populateParamsDetails fetches detailed information for parameters from the Ceph cluster
func (c *Config) populateParamsDetails(ctx context.Context, radosSvc *rados.Svc, params []string, logger *zerolog.Logger) {
	for _, name := range params {
		paramInfo, err := fetchParamDetailFromCluster(ctx, radosSvc, name)
		if err != nil {
			logger.Err(err).Str("param", name).Msg("Failed to fetch parameter details")
			continue
		}

		// Update the parameter info in our map
		c.params[name] = paramInfo
	}
}

// fetchParamDetailFromCluster fetches detailed information for a single parameter from the Ceph cluster
func fetchParamDetailFromCluster(ctx context.Context, radosSvc *rados.Svc, paramName string) (ConfigParamInfo, error) {
	// Execute 'ceph config help' command for this parameter
	// Note: The cmd string for 'config help' uses 'key' and not 'name'
	monCmd := fmt.Sprintf(`{"prefix": "config help", "key": "%s", "format": "json"}`, paramName)
	cmdRes, err := radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		return ConfigParamInfo{}, fmt.Errorf("failed to execute 'config help' command: %w", err)
	}

	// Parse the result into our ConfigParamInfo structure
	paramInfo, err := parseConfigHelpResponse(cmdRes, paramName)
	if err != nil {
		return ConfigParamInfo{}, fmt.Errorf("failed to parse config help response: %w", err)
	}

	return paramInfo, nil
}

// parseConfigHelpResponse parses the JSON response from 'ceph config help' command
func parseConfigHelpResponse(jsonResponse []byte, paramName string) (ConfigParamInfo, error) {
	var paramInfo ConfigParamInfo
	err := json.Unmarshal(jsonResponse, &paramInfo)
	if err != nil {
		return ConfigParamInfo{}, fmt.Errorf("failed to unmarshal config help response: %w", err)
	}
	return paramInfo, nil
}

// Search searches for configuration parameters according to the query parameters
func (c *Config) Search(query QueryParams) []ConfigParamInfo {
	// Set default values
	if query.Sort == 0 {
		query.Sort = pb.SearchConfigRequest_NAME
	}
	if query.Order == 0 {
		query.Order = pb.SearchConfigRequest_ASC
	}

	// Remove pre-allocation optimization
	var result []ConfigParamInfo

	// Convert full text to lowercase once if needed
	var fullTextLower string
	if query.FullText != "" {
		fullTextLower = strings.ToLower(query.FullText)
	}

	// Filter parameters
	for _, info := range c.params {
		if !c.matchesService(info, query.Service) {
			continue
		}
		if !c.matchesName(info, query.Name) {
			continue
		}
		if !c.matchesLevel(info, query.Level) {
			continue
		}
		if !c.matchesFullText(info, fullTextLower) {
			continue
		}
		result = append(result, info)
	}

	// Sort results using Go's built-in sort
	c.sortResults(result, query.Sort, query.Order)

	return result
}

// matchesService checks if the parameter matches the service filter
func (c *Config) matchesService(info ConfigParamInfo, service pb.SearchConfigRequest_ServiceType) bool {
	if service == pb.SearchConfigRequest_COMMON {
		return true
	}

	// Map enum to canonical string representation
	var serviceStr string
	switch service {
	case pb.SearchConfigRequest_COMMON:
		serviceStr = "common"
	case pb.SearchConfigRequest_MON:
		serviceStr = "mon"
	case pb.SearchConfigRequest_MDS:
		serviceStr = "mds"
	case pb.SearchConfigRequest_OSD:
		serviceStr = "osd"
	case pb.SearchConfigRequest_MGR:
		serviceStr = "mgr"
	case pb.SearchConfigRequest_RGW:
		serviceStr = "rgw"
	case pb.SearchConfigRequest_RBD:
		serviceStr = "rbd"
	case pb.SearchConfigRequest_RBD_MIRROR:
		serviceStr = "rbd-mirror"
	case pb.SearchConfigRequest_IMMUTABLE_OBJECT_CACHE:
		serviceStr = "immutable-object-cache"
	case pb.SearchConfigRequest_MDS_CLIENT:
		serviceStr = "mds_client"
	case pb.SearchConfigRequest_CEPHFS_MIRROR:
		serviceStr = "cephfs-mirror"
	case pb.SearchConfigRequest_CEPH_EXPORTER:
		serviceStr = "ceph-exporter"
	default:
		serviceStr = service.String()
	}

	for _, svc := range info.Services {
		if strings.EqualFold(svc, serviceStr) {
			return true
		}
	}
	return false
}

// matchesName checks if the parameter matches the name filter
func (c *Config) matchesName(info ConfigParamInfo, name string) bool {
	if name == "" {
		return true
	}
	return matchWildcard(info.Name, name)
}

// matchesLevel checks if the parameter matches the level filter
func (c *Config) matchesLevel(info ConfigParamInfo, level pb.SearchConfigRequest_ConfigLevel) bool {
	if level == pb.SearchConfigRequest_BASIC {
		return strings.EqualFold(info.Level, "basic")
	}
	if level == pb.SearchConfigRequest_ADVANCED {
		return strings.EqualFold(info.Level, "advanced")
	}
	if level == pb.SearchConfigRequest_DEV {
		return strings.EqualFold(info.Level, "dev")
	}
	return true
}

// matchesFullText checks if the parameter matches the full-text search
func (c *Config) matchesFullText(info ConfigParamInfo, fullTextLower string) bool {
	if fullTextLower == "" {
		return true
	}

	// Check text fields
	if strings.Contains(strings.ToLower(info.Name), fullTextLower) ||
		strings.Contains(strings.ToLower(info.Type), fullTextLower) ||
		strings.Contains(strings.ToLower(info.Level), fullTextLower) ||
		strings.Contains(strings.ToLower(info.Desc), fullTextLower) ||
		strings.Contains(strings.ToLower(info.LongDesc), fullTextLower) ||
		strings.Contains(strings.ToLower(fmt.Sprint(info.Default)), fullTextLower) ||
		strings.Contains(strings.ToLower(fmt.Sprint(info.DaemonDefault)), fullTextLower) {
		return true
	}

	// Check arrays
	for _, tag := range info.Tags {
		if strings.Contains(strings.ToLower(tag), fullTextLower) {
			return true
		}
	}
	for _, svc := range info.Services {
		if strings.Contains(strings.ToLower(svc), fullTextLower) {
			return true
		}
	}

	return false
}

// sortResults sorts the results using Go's built-in sort
func (c *Config) sortResults(results []ConfigParamInfo, field pb.SearchConfigRequest_SortField, order pb.SearchConfigRequest_SortOrder) {
	if len(results) <= 1 {
		return
	}

	// Create a sort.Interface implementation
	sort.Slice(results, func(i, j int) bool {
		switch field {
		case pb.SearchConfigRequest_NAME:
			if order == pb.SearchConfigRequest_ASC {
				return results[i].Name < results[j].Name
			}
			return results[i].Name > results[j].Name
		case pb.SearchConfigRequest_TYPE:
			if order == pb.SearchConfigRequest_ASC {
				return results[i].Type < results[j].Type
			}
			return results[i].Type > results[j].Type
		case pb.SearchConfigRequest_LEVEL:
			if order == pb.SearchConfigRequest_ASC {
				return results[i].Level < results[j].Level
			}
			return results[i].Level > results[j].Level
		case pb.SearchConfigRequest_SERVICE:
			iService := ""
			jService := ""
			if len(results[i].Services) > 0 {
				iService = results[i].Services[0]
			}
			if len(results[j].Services) > 0 {
				jService = results[j].Services[0]
			}
			if order == pb.SearchConfigRequest_ASC {
				return iService < jService
			}
			return iService > jService
		default:
			return false
		}
	})
}

// matchWildcard checks if a string matches a wildcard pattern
func matchWildcard(s, pattern string) bool {
	matched, _ := filepath.Match(pattern, s)
	return matched
}
