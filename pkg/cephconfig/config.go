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
	Service  *pb.ConfigParam_ServiceType
	Level    *pb.ConfigParam_ConfigLevel
	Name     *string
	FullText *string
	Sort     *pb.SearchConfigRequest_SortField
	Order    *pb.SearchConfigRequest_SortOrder
	Type     *pb.ConfigParam_ParamType
}

// ConfigParams is a slice of parameter information
type ConfigParams []ConfigParamInfo

// Config manages Ceph configuration parameters
type Config struct {
	params ConfigParams
}

// serviceTypeMap maps service enum values to their string representation
var serviceTypeMap = map[pb.ConfigParam_ServiceType]string{
	pb.ConfigParam_common:                 "common",
	pb.ConfigParam_mon:                    "mon",
	pb.ConfigParam_mds:                    "mds",
	pb.ConfigParam_osd:                    "osd",
	pb.ConfigParam_mgr:                    "mgr",
	pb.ConfigParam_rgw:                    "rgw",
	pb.ConfigParam_rbd:                    "rbd",
	pb.ConfigParam_rbd_mirror:             "rbd-mirror",
	pb.ConfigParam_immutable_object_cache: "immutable-object-cache",
	pb.ConfigParam_mds_client:             "mds_client",
	pb.ConfigParam_cephfs_mirror:          "cephfs-mirror",
	pb.ConfigParam_ceph_exporter:          "ceph-exporter",
}

// ServiceStringToEnum maps service string to enum value
var ServiceStringToEnum = func() map[string]pb.ConfigParam_ServiceType {
	m := make(map[string]pb.ConfigParam_ServiceType)
	for k, v := range serviceTypeMap {
		m[v] = k
	}
	return m
}()

// loadParamsSlice loads all Ceph configuration params from the embedded JSON file into a sorted slice
// NOTE: config-index.json should be sorted by the "name" field already
func loadParamsSlice(ctx context.Context) ([]ConfigParamInfo, error) {
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

	return jsonArray, nil
}

// mergeParams merges two sorted lists of config params and names
// - sortedBase: sorted slice of ConfigParamInfo (from JSON, must be sorted by Name)
// - sortedCluster: sorted slice of names (from cluster, must be sorted)
// - fetchNew: function to fetch details for new params (present in cluster but not in base)
func mergeParams(
	ctx context.Context,
	sortedBase []ConfigParamInfo,
	sortedCluster []string,
	fetchNew func(ctx context.Context, name string) (ConfigParamInfo, error),
) ([]ConfigParamInfo, error) {
	result := make([]ConfigParamInfo, 0, len(sortedCluster))
	i, j := 0, 0
	for i < len(sortedBase) && j < len(sortedCluster) {
		a, b := sortedBase[i].Name, sortedCluster[j]
		switch {
		case a == b:
			// Param exists in both base and cluster: keep from base
			result = append(result, sortedBase[i])
			i++
			j++
		case a > b:
			// New param in cluster (not in base): fetch details and add
			paramInfo, err := fetchNew(ctx, b)
			if err != nil {
				return nil, err
			}
			result = append(result, paramInfo)
			j++
		case a < b:
			// Param in base but not in cluster: skip (remove)
			i++
		}
	}
	// Any remaining names in cluster are new params
	for ; j < len(sortedCluster); j++ {
		paramInfo, err := fetchNew(ctx, sortedCluster[j])
		if err != nil {
			return nil, err
		}
		result = append(result, paramInfo)
	}
	return result, nil
}

// NewConfig creates a new Config instance and updates parameters from the cluster synchronously
func NewConfig(ctx context.Context, radosSvc *rados.Svc, skipUpdate bool) (*Config, error) {
	logger := zerolog.Ctx(ctx)
	sortedBase, err := loadParamsSlice(ctx)
	if err != nil {
		return nil, err
	}

	if skipUpdate {
		return &Config{params: sortedBase}, nil
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

	sort.Strings(clusterParams)

	fetchNew := func(ctx context.Context, name string) (ConfigParamInfo, error) {
		return fetchParamDetailFromCluster(ctx, radosSvc, name)
	}

	result, err := mergeParams(ctx, sortedBase, clusterParams, fetchNew)
	if err != nil {
		return nil, err
	}

	logger.Info().
		Int("total_params", len(result)).
		Msg("Updated Ceph configuration parameters from cluster (single-pass)")

	return &Config{params: result}, nil
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
func matchesService(info ConfigParamInfo, service *pb.ConfigParam_ServiceType) bool {
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
func matchesLevel(info ConfigParamInfo, level *pb.ConfigParam_ConfigLevel) bool {
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
func matchesType(info ConfigParamInfo, paramType *pb.ConfigParam_ParamType) bool {
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
		default:
			return false
		}
	})
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
