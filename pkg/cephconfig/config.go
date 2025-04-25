package cephconfig

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/rs/zerolog"
)

//go:embed config-index.json
var configIndexFile embed.FS

// ServiceType represents a Ceph service type
type ServiceType string

const (
	ServiceMon     ServiceType = "mon"
	ServiceOSD     ServiceType = "osd"
	ServiceMDS     ServiceType = "mds"
	ServiceRGW     ServiceType = "rgw"
	ServiceMgr     ServiceType = "mgr"
	ServiceCommon  ServiceType = "common"
	ServiceClient  ServiceType = "client"
	ServiceUnknown ServiceType = "unknown"
)

// ConfigParamLevel represents the configuration parameter level
type ConfigParamLevel string

const (
	LevelBasic        ConfigParamLevel = "basic"
	LevelAdvanced     ConfigParamLevel = "advanced"
	LevelDeveloper    ConfigParamLevel = "developer"
	LevelExperimental ConfigParamLevel = "experimental"
	LevelUnknown      ConfigParamLevel = "unknown"
)

// SortOrder represents the sort order
type SortOrder string

const (
	SortOrderAsc  SortOrder = "asc"
	SortOrderDesc SortOrder = "desc"
)

// SortField represents the field to sort by
type SortField string

const (
	SortFieldName    SortField = "name"
	SortFieldType    SortField = "type"
	SortFieldService SortField = "service"
	SortFieldLevel   SortField = "level"
)

// ConfigParamInfo represents the help data for a Ceph configuration parameter
type ConfigParamInfo struct {
	Name               string           `json:"name"`
	Type               string           `json:"type"`
	Level              ConfigParamLevel `json:"level"`
	Desc               string           `json:"desc"`
	LongDesc           string           `json:"long_desc"`
	Default            interface{}      `json:"default"`
	DaemonDefault      interface{}      `json:"daemon_default"`
	Tags               []string         `json:"tags"`
	Services           []string         `json:"services"`
	SeeAlso            []string         `json:"see_also"`
	EnumValues         []string         `json:"enum_values"`
	Min                interface{}      `json:"min"`
	Max                interface{}      `json:"max"`
	CanUpdateAtRuntime bool             `json:"can_update_at_runtime"`
	Flags              []string         `json:"flags"`
}

// QueryParams contains the parameters for config search
type QueryParams struct {
	Service  ServiceType      `json:"service"`
	Name     string           `json:"name"`
	FullText string           `json:"full_text"`
	Level    ConfigParamLevel `json:"level"`
	Sort     SortField        `json:"sort"`
	Order    SortOrder        `json:"order"`
}

// ConfigParams is a map of parameter names to their information
type ConfigParams map[string]ConfigParamInfo

// Config manages Ceph configuration parameters
type Config struct {
	params     ConfigParams
	mu         sync.RWMutex
	isUpdating bool
}

// loadConfigParams loads all Ceph configuration parameters from the embedded JSON file
func loadConfigParams() (ConfigParams, error) {
	// Open the embedded config index file
	logger := zerolog.DefaultContextLogger.Info()
	logger.Msg("EMBED : Loading Ceph configuration parameters from embedded JSON file AIR mode")

	// Read the file data
	data, err := configIndexFile.ReadFile("config-index.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded config-index.json: %w", err)
	}

	// Parse the JSON structure (array of objects with single key)
	var jsonArray []map[string]ConfigParamInfo
	err = json.Unmarshal(data, &jsonArray)
	if err != nil {
		logger.Msg("EMBED : FAILED UNMARSHALING CONFIG INDEX JSON")
		return nil, fmt.Errorf("failed to unmarshal config index JSON: %w", err)

	}

	// Convert to our ConfigParams map format
	configParams := make(ConfigParams)
	for _, item := range jsonArray {
		for name, info := range item {
			configParams[name] = info
		}
	}

	return configParams, nil
}

// NewConfig creates a new Config instance
func NewConfig() (*Config, error) {
	params, err := loadConfigParams()
	if err != nil {
		return nil, err
	}
	return &Config{
		params:     params,
		isUpdating: false,
	}, nil
}

// UpdateConfigFromCluster updates the configuration parameters by querying the Ceph cluster
// This is done in the background to avoid slowing down the initialization process
func (c *Config) UpdateConfigFromCluster(ctx context.Context, radosSvc *rados.Svc) {
	// If already updating, don't start again
	if c.isUpdating {
		return
	}

	c.isUpdating = true

	go func() {
		defer func() { c.isUpdating = false }()

		logger := zerolog.Ctx(ctx)
		logger.Info().Msg("Starting background update of Ceph configuration parameters from cluster")

		// Execute 'ceph config ls' command to get current configuration
		const monCmd = `{"prefix": "config ls", "format": "json"}`
		cmdRes, err := radosSvc.ExecMon(ctx, monCmd)
		if err != nil {
			logger.Err(err).Msg("Failed to execute 'config ls' command")
			return
		}

		// Parse the result - it's an array of parameter names
		var clusterParams []string
		err = json.Unmarshal(cmdRes, &clusterParams)
		if err != nil {
			logger.Err(err).Msg("Failed to unmarshal config ls response")
			return
		}

		// Create a map for quick lookup of parameter names
		clusterParamsMap := make(map[string]bool)
		for _, param := range clusterParams {
			clusterParamsMap[param] = true
		}

		// Lock for writing
		c.mu.Lock()
		defer c.mu.Unlock()

		// Get the current parameter names
		currentParamsMap := make(map[string]bool)
		for name := range c.params {
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
			c.params[name] = ConfigParamInfo{
				Name:     name,
				Level:    LevelUnknown,
				Type:     "unknown",
				Services: []string{"unknown"},
			}
		}

		// Remove parameters that don't exist in the cluster
		for _, name := range paramsToRemove {
			delete(c.params, name)
		}

		// Populate details for new parameters
		if len(paramsToAdd) > 0 {
			c.populateParamsDetails(ctx, radosSvc, paramsToAdd, logger)
		}

		logger.Info().
			Int("total_params", len(c.params)).
			Int("added", len(paramsToAdd)).
			Int("removed", len(paramsToRemove)).
			Msg("Updated Ceph configuration parameters from cluster")
	}()
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
	// Define a temporary struct to match the JSON response format
	type ConfigHelpResponse struct {
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

	var response ConfigHelpResponse
	err := json.Unmarshal(jsonResponse, &response)
	if err != nil {
		return ConfigParamInfo{}, fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	// Convert level string to ConfigParamLevel
	level := mapStringToConfigParamLevel(response.Level)

	// Convert the response to our ConfigParamInfo structure
	paramInfo := ConfigParamInfo{
		Name:               response.Name,
		Type:               response.Type,
		Level:              level,
		Desc:               response.Desc,
		LongDesc:           response.LongDesc,
		Default:            response.Default,
		DaemonDefault:      response.DaemonDefault,
		Tags:               response.Tags,
		Services:           response.Services,
		SeeAlso:            response.SeeAlso,
		EnumValues:         response.EnumValues,
		Min:                response.Min,
		Max:                response.Max,
		CanUpdateAtRuntime: response.CanUpdateAtRuntime,
		Flags:              response.Flags,
	}

	return paramInfo, nil
}

// mapStringToConfigParamLevel maps a string level to ConfigParamLevel enum
func mapStringToConfigParamLevel(level string) ConfigParamLevel {
	switch strings.ToLower(level) {
	case "basic":
		return LevelBasic
	case "advanced":
		return LevelAdvanced
	case "developer":
		return LevelDeveloper
	case "experimental":
		return LevelExperimental
	default:
		return LevelUnknown
	}
}

// Search searches for configuration parameters according to the query parameters
func (c *Config) Search(query QueryParams) []ConfigParamInfo {
	// Acquire read lock
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := []ConfigParamInfo{}

	// Default sort
	if query.Sort == "" {
		query.Sort = SortFieldName
	}

	// Default order
	if query.Order == "" {
		query.Order = SortOrderAsc
	}

	// Filter parameters
	for _, info := range c.params {
		// If service filter is set, check if the parameter belongs to this service
		if query.Service != "" {
			serviceMatch := false
			for _, svc := range info.Services {
				if ServiceType(svc) == query.Service {
					serviceMatch = true
					break
				}
			}
			if !serviceMatch {
				continue
			}
		}

		// If name filter is set, check if the parameter name matches the pattern
		if query.Name != "" {
			if !matchWildcard(info.Name, query.Name) {
				continue
			}
		}

		// If level filter is set, check if the parameter has this level
		if query.Level != "" {
			if info.Level != query.Level {
				continue
			}
		}

		// If full-text search is set, check if any field contains this text
		if query.FullText != "" {
			fullTextLower := strings.ToLower(query.FullText)
			found := false

			// Check in all text fields
			if strings.Contains(strings.ToLower(info.Name), fullTextLower) ||
				strings.Contains(strings.ToLower(info.Type), fullTextLower) ||
				strings.Contains(strings.ToLower(string(info.Level)), fullTextLower) ||
				strings.Contains(strings.ToLower(info.Desc), fullTextLower) ||
				strings.Contains(strings.ToLower(info.LongDesc), fullTextLower) ||
				strings.Contains(strings.ToLower(fmt.Sprint(info.Default)), fullTextLower) ||
				strings.Contains(strings.ToLower(fmt.Sprint(info.DaemonDefault)), fullTextLower) {
				found = true
			}

			// Check in arrays
			if !found {
				for _, tag := range info.Tags {
					if strings.Contains(strings.ToLower(tag), fullTextLower) {
						found = true
						break
					}
				}
			}
			if !found {
				for _, svc := range info.Services {
					if strings.Contains(strings.ToLower(svc), fullTextLower) {
						found = true
						break
					}
				}
			}

			if !found {
				continue
			}
		}

		// Add to results
		result = append(result, info)
	}

	// Sort results
	sortConfigParams(result, query.Sort, query.Order)

	return result
}

// matchWildcard checks if a string matches a wildcard pattern
func matchWildcard(s, pattern string) bool {
	matched, _ := filepath.Match(pattern, s)
	return matched
}

// sortConfigParams sorts configuration parameters by the specified field and order
func sortConfigParams(params []ConfigParamInfo, field SortField, order SortOrder) {
	// Sort by field
	switch field {
	case SortFieldName:
		if order == SortOrderAsc {
			// Sort by name ascending
			for i := 0; i < len(params); i++ {
				for j := i + 1; j < len(params); j++ {
					if params[i].Name > params[j].Name {
						params[i], params[j] = params[j], params[i]
					}
				}
			}
		} else {
			// Sort by name descending
			for i := 0; i < len(params); i++ {
				for j := i + 1; j < len(params); j++ {
					if params[i].Name < params[j].Name {
						params[i], params[j] = params[j], params[i]
					}
				}
			}
		}
	case SortFieldType:
		if order == SortOrderAsc {
			// Sort by type ascending
			for i := 0; i < len(params); i++ {
				for j := i + 1; j < len(params); j++ {
					if params[i].Type > params[j].Type {
						params[i], params[j] = params[j], params[i]
					}
				}
			}
		} else {
			// Sort by type descending
			for i := 0; i < len(params); i++ {
				for j := i + 1; j < len(params); j++ {
					if params[i].Type < params[j].Type {
						params[i], params[j] = params[j], params[i]
					}
				}
			}
		}
	case SortFieldLevel:
		if order == SortOrderAsc {
			// Sort by level ascending
			for i := 0; i < len(params); i++ {
				for j := i + 1; j < len(params); j++ {
					if string(params[i].Level) > string(params[j].Level) {
						params[i], params[j] = params[j], params[i]
					}
				}
			}
		} else {
			// Sort by level descending
			for i := 0; i < len(params); i++ {
				for j := i + 1; j < len(params); j++ {
					if string(params[i].Level) < string(params[j].Level) {
						params[i], params[j] = params[j], params[i]
					}
				}
			}
		}
	case SortFieldService:
		// Sort by first service in the list
		if order == SortOrderAsc {
			// Sort by service ascending
			for i := 0; i < len(params); i++ {
				for j := i + 1; j < len(params); j++ {
					iService := ""
					jService := ""
					if len(params[i].Services) > 0 {
						iService = params[i].Services[0]
					}
					if len(params[j].Services) > 0 {
						jService = params[j].Services[0]
					}
					if iService > jService {
						params[i], params[j] = params[j], params[i]
					}
				}
			}
		} else {
			// Sort by service descending
			for i := 0; i < len(params); i++ {
				for j := i + 1; j < len(params); j++ {
					iService := ""
					jService := ""
					if len(params[i].Services) > 0 {
						iService = params[i].Services[0]
					}
					if len(params[j].Services) > 0 {
						jService = params[j].Services[0]
					}
					if iService < jService {
						params[i], params[j] = params[j], params[i]
					}
				}
			}
		}
	}
}

// GetParamInfo retrieves information about a specific configuration parameter
func GetParamInfo(name string, params ConfigParams) (ConfigParamInfo, bool) {
	info, ok := params[name]
	return info, ok
}

// FilterParamsByService returns parameters that apply to a specific service
func FilterParamsByService(service string, params ConfigParams) ConfigParams {
	result := make(ConfigParams)

	for name, info := range params {
		for _, svc := range info.Services {
			if svc == service {
				result[name] = info
				break
			}
		}
	}

	return result
}

// FilterParamsByTag returns parameters that have a specific tag
func FilterParamsByTag(tag string, params ConfigParams) ConfigParams {
	result := make(ConfigParams)

	for name, info := range params {
		for _, t := range info.Tags {
			if t == tag {
				result[name] = info
				break
			}
		}
	}

	return result
}

// FilterParamsByLevel returns parameters of a specific level
func FilterParamsByLevel(level string, params ConfigParams) ConfigParams {
	result := make(ConfigParams)
	for name, info := range params {
		if string(info.Level) == level {
			result[name] = info
		}
	}

	return result
}

// GetRuntimeConfigurableParams returns all parameters that can be updated at runtime
func GetRuntimeConfigurableParams(params ConfigParams) ConfigParams {
	result := make(ConfigParams)

	for name, info := range params {
		if info.CanUpdateAtRuntime {
			result[name] = info
		}
	}

	return result
}
