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
	Min                interface{} `json:"min"`
	Max                interface{} `json:"max"`
	CanUpdateAtRuntime bool        `json:"can_update_at_runtime"`
	Flags              []string    `json:"flags"`
}

// QueryParams contains the parameters for config search
type QueryParams struct {
	Service  *pb.SearchConfigRequest_ServiceType
	Level    *pb.SearchConfigRequest_ConfigLevel
	Name     *string
	FullText *string
	Sort     *pb.SearchConfigRequest_SortField
	Order    *pb.SearchConfigRequest_SortOrder
	Type     *pb.SearchConfigRequest_ParamType
}

// ConfigParams is a slice of parameter information
type ConfigParams []ConfigParamInfo

// Config manages Ceph configuration parameters
type Config struct {
	params ConfigParams
}

// serviceTypeMap maps service enum values to their string representation
var serviceTypeMap = map[pb.SearchConfigRequest_ServiceType]string{
	pb.SearchConfigRequest_COMMON:                 "common",
	pb.SearchConfigRequest_MON:                    "mon",
	pb.SearchConfigRequest_MDS:                    "mds",
	pb.SearchConfigRequest_OSD:                    "osd",
	pb.SearchConfigRequest_MGR:                    "mgr",
	pb.SearchConfigRequest_RGW:                    "rgw",
	pb.SearchConfigRequest_RBD:                    "rbd",
	pb.SearchConfigRequest_RBD_MIRROR:             "rbd-mirror",
	pb.SearchConfigRequest_IMMUTABLE_OBJECT_CACHE: "immutable-object-cache",
	pb.SearchConfigRequest_MDS_CLIENT:             "mds_client",
	pb.SearchConfigRequest_CEPHFS_MIRROR:          "cephfs-mirror",
	pb.SearchConfigRequest_CEPH_EXPORTER:          "ceph-exporter",
}

// loadParamsMap loads all Ceph configuration params from the embedded JSON file into a map
func loadParamsMap(ctx context.Context) (map[string]ConfigParamInfo, error) {
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
	configParams := make(map[string]ConfigParamInfo)
	for _, info := range jsonArray {
		configParams[info.Name] = info
	}

	return configParams, nil
}

// NewConfig creates a new Config instance and updates parameters from the cluster synchronously
func NewConfig(ctx context.Context, radosSvc *rados.Svc, skipUpdate bool) (*Config, error) {
	logger := zerolog.Ctx(ctx)
	paramMap, err := loadParamsMap(ctx)

	if err != nil {
		return nil, err
	}

	if skipUpdate {
		return &Config{params: defaultSort(paramMap)}, nil
	}

	const monCmd = `{"prefix": "config ls", "format": "json"}`
	cmdRes, err := radosSvc.ExecMon(ctx, monCmd)
	if err != nil {
		logger.Err(err).Msg("Failed to execute 'config ls' command")
		return nil, err
	}

	var clusterParams []string
	err = json.Unmarshal(cmdRes, &clusterParams)
	if err != nil {
		logger.Err(err).Msg("Failed to unmarshal config ls response")
		return nil, err
	}

	clusterParamsSet := make(map[string]struct{}, len(clusterParams))
	for _, name := range clusterParams {
		clusterParamsSet[name] = struct{}{}
	}

	// Remove params not in cluster
	for name := range paramMap {
		if _, found := clusterParamsSet[name]; !found {
			delete(paramMap, name)
		}
	}
	// Add new params from cluster
	for _, name := range clusterParams {
		if _, found := paramMap[name]; !found {
			paramInfo, err := fetchParamDetailFromCluster(ctx, radosSvc, name)
			if err != nil {
				logger.Err(err).Str("param", name).Msg("Failed to fetch parameter details")
				return nil, err
			}
			paramMap[name] = paramInfo
		}
	}

	params := defaultSort(paramMap)
	logger.Info().
		Int("total_params", len(params)).
		Msg("Updated Ceph configuration parameters from cluster")

	return &Config{params: params}, nil
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

	var paramInfo ConfigParamInfo
	err = json.Unmarshal(cmdRes, &paramInfo)
	if err != nil {
		return ConfigParamInfo{}, fmt.Errorf("failed to parse config help response: %w", err)
	}

	return paramInfo, nil
}

// Search searches for configuration parameters according to the query parameters
func (c *Config) Search(query QueryParams) []ConfigParamInfo {
	var result []ConfigParamInfo

	var fullTextLower string
	if query.FullText != nil && *query.FullText != "" {
		fullTextLower = strings.ToLower(*query.FullText)
	}

	// If Name is set and does not contain wildcards, return immediately after match
	uniqueName := query.Name != nil && *query.Name != "" && !strings.ContainsAny(*query.Name, "*?[]")

	for _, info := range c.params {
		if !matchesService(info, query.Service) {
			continue
		}
		if !matchesLevel(info, query.Level) {
			continue
		}
		if !matchesName(info, query.Name) {
			continue
		}
		if !matchesType(info, query.Type) {
			continue
		}
		if !matchesFullText(info, fullTextLower) {
			continue
		}
		result = append(result, info)
		if uniqueName {
			break
		}
	}

	field := pb.SearchConfigRequest_NAME
	if query.Sort != nil {
		field = *query.Sort
	}
	order := pb.SearchConfigRequest_ASC
	if query.Order != nil {
		order = *query.Order
	}

	sortResults(result, field, order)
	return result
}

// matchesService checks if the parameter matches the service filter
func matchesService(info ConfigParamInfo, service *pb.SearchConfigRequest_ServiceType) bool {
	if service == nil {
		return true
	}
	serviceStr, found := serviceTypeMap[*service]
	if !found {
		serviceStr = service.String()
	}

	for _, svc := range info.Services {
		if strings.EqualFold(svc, serviceStr) {
			return true
		}
	}
	return false
}

// matchesLevel checks if the parameter matches the level filter
func matchesLevel(info ConfigParamInfo, level *pb.SearchConfigRequest_ConfigLevel) bool {
	if level == nil {
		return true
	}

	return strings.EqualFold(info.Level, level.String())
}

// matchesName checks if the parameter matches the name filter
func matchesName(info ConfigParamInfo, name *string) bool {
	if name == nil || *name == "" {
		return true
	}
	return matchWildcard(info.Name, *name)
}

// matchesType checks if the parameter matches the type filter
func matchesType(info ConfigParamInfo, paramType *pb.SearchConfigRequest_ParamType) bool {
	if paramType == nil {
		return true
	}

	return strings.EqualFold(info.Type, strings.ToLower(paramType.String()))
}

// matchesFullText checks if the parameter matches the full-text search
func matchesFullText(info ConfigParamInfo, fullTextLower string) bool {
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

// matchWildcard checks if a string matches a wildcard pattern
func matchWildcard(s, pattern string) bool {
	matched, _ := filepath.Match(pattern, s)
	return matched
}

// sortResults sorts the results using Go's built-in sort
func sortResults(results []ConfigParamInfo, field pb.SearchConfigRequest_SortField, order pb.SearchConfigRequest_SortOrder) {
	if len(results) <= 1 {
		return
	}

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

func defaultSort(paramMap map[string]ConfigParamInfo) []ConfigParamInfo {
	params := make([]ConfigParamInfo, 0, len(paramMap))
	for _, v := range paramMap {
		params = append(params, v)
	}
	sortResults(params, pb.SearchConfigRequest_NAME, pb.SearchConfigRequest_ASC)
	return params
}

// Helper function to parse min/max values from interface{} to *float64 for the API response.
// Needed to handle the case for empty strings, which implies undefined min & max.
func ParseMinMax(val interface{}) *float64 {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case float64:
		return &v
	case string:
		if v == "" {
			return nil
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return &f
		}
		return nil
	}
	return nil
}
