package cephconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
)

func TestConfig_Search_FilteringAndSorting(t *testing.T) {
	req := require.New(t)
	ctx := context.Background()
	cfg, err := NewConfig(ctx, nil, true) // skipUpdate: true for testing
	req.NoError(err, "failed to create config: %v", err)

	sortName := pb.SearchConfigRequest_SortField(pb.SearchConfigRequest_NAME)
	sortType := pb.SearchConfigRequest_SortField(pb.SearchConfigRequest_TYPE)
	sortLevel := pb.SearchConfigRequest_SortField(pb.SearchConfigRequest_LEVEL)
	sortAsc := pb.SearchConfigRequest_SortOrder(pb.SearchConfigRequest_ASC)
	sortDesc := pb.SearchConfigRequest_SortOrder(pb.SearchConfigRequest_DESC)
	serviceOsd := pb.SearchConfigRequest_ServiceType(pb.SearchConfigRequest_OSD)
	levelBasic := pb.SearchConfigRequest_ConfigLevel(pb.SearchConfigRequest_BASIC)
	typeStr := pb.SearchConfigRequest_ParamType(pb.SearchConfigRequest_STR)
	typeInt := pb.SearchConfigRequest_ParamType(pb.SearchConfigRequest_INT)

	tests := []struct {
		name   string
		query  QueryParams
		assert func([]ConfigParamInfo) error
	}{
		{
			name:  "Filter by name wildcard",
			query: QueryParams{Name: "mon_*"},
			assert: func(results []ConfigParamInfo) error {
				for _, r := range results {
					if !strings.HasPrefix(r.Name, "mon_") {
						return fmt.Errorf("unexpected param: %s", r.Name)
					}
				}
				return nil
			},
		},
		{
			name: "Sort by name ascending",
			query: QueryParams{
				Sort:  &sortName,
				Order: &sortAsc,
			},
			assert: func(results []ConfigParamInfo) error {
				for i := 1; i < len(results); i++ {
					if results[i-1].Name > results[i].Name {
						return fmt.Errorf("not sorted: %s > %s", results[i-1].Name, results[i].Name)
					}
				}
				return nil
			},
		},
		{
			name:  "Filter by service OSD",
			query: QueryParams{Service: &serviceOsd},
			assert: func(results []ConfigParamInfo) error {
				for _, r := range results {
					found := false
					for _, svc := range r.Services {
						if strings.EqualFold(svc, "osd") {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("param %s does not have service 'osd'", r.Name)
					}
				}
				return nil
			},
		},
		{
			name:  "Filter by level basic",
			query: QueryParams{Level: &levelBasic},
			assert: func(results []ConfigParamInfo) error {
				for _, r := range results {
					if !strings.EqualFold(r.Level, "basic") {
						return fmt.Errorf("param %s is not level 'basic'", r.Name)
					}
				}
				return nil
			},
		},
		{
			name: "Sort by type descending",
			query: QueryParams{
				Sort:  &sortType,
				Order: &sortDesc,
			},
			assert: func(results []ConfigParamInfo) error {
				for i := 1; i < len(results); i++ {
					if results[i-1].Type < results[i].Type {
						return fmt.Errorf("not sorted descending: %s < %s", results[i-1].Type, results[i].Type)
					}
				}
				return nil
			},
		},
		{
			name: "Sort by level ascending",
			query: QueryParams{
				Sort:  &sortLevel,
				Order: &sortAsc,
			},
			assert: func(results []ConfigParamInfo) error {
				for i := 1; i < len(results); i++ {
					if results[i-1].Level > results[i].Level {
						return fmt.Errorf("not sorted ascending: %s > %s", results[i-1].Level, results[i].Level)
					}
				}
				return nil
			},
		},
		{
			name:  "Filter by type str",
			query: QueryParams{Type: &typeStr},
			assert: func(results []ConfigParamInfo) error {
				for _, r := range results {
					if !strings.EqualFold(r.Type, "str") {
						return fmt.Errorf("param %s is not type 'str'", r.Name)
					}
				}
				return nil
			},
		},
		{
			name:  "Filter by type int",
			query: QueryParams{Type: &typeInt},
			assert: func(results []ConfigParamInfo) error {
				for _, r := range results {
					if !strings.EqualFold(r.Type, "int") {
						return fmt.Errorf("param %s is not type 'int'", r.Name)
					}
				}
				return nil
			},
		},
		{
			name: "Combined filter: service and type",
			query: QueryParams{
				Service: &serviceOsd,
				Type:    &typeInt,
			},
			assert: func(results []ConfigParamInfo) error {
				for _, r := range results {
					found := false
					for _, svc := range r.Services {
						if strings.EqualFold(svc, "osd") {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("param %s does not have service 'osd'", r.Name)
					}
					if !strings.EqualFold(r.Type, "int") {
						return fmt.Errorf("param %s is not type 'int'", r.Name)
					}
				}
				return nil
			},
		},
		{
			name: "Combined filter: name and service",
			query: QueryParams{
				Name:    "osd_*",
				Service: &serviceOsd,
			},
			assert: func(results []ConfigParamInfo) error {
				for _, r := range results {
					if !strings.HasPrefix(r.Name, "osd_") {
						return fmt.Errorf("unexpected param: %s", r.Name)
					}
					found := false
					for _, svc := range r.Services {
						if strings.EqualFold(svc, "osd") {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("param %s does not have service 'osd'", r.Name)
					}
				}
				return nil
			},
		},
		{
			name:  "No results for unlikely filter",
			query: QueryParams{Name: "this_param_does_not_exist"},
			assert: func(results []ConfigParamInfo) error {
				if len(results) != 0 {
					return fmt.Errorf("expected no results, got %d", len(results))
				}
				return nil
			},
		},
		{
			name:  "Exact name match returns single result",
			query: QueryParams{Name: "fsid"},
			assert: func(results []ConfigParamInfo) error {
				if len(results) != 1 {
					return fmt.Errorf("expected 1 result, got %d", len(results))
				}
				if results[0].Name != "fsid" {
					return fmt.Errorf("expected result with name 'fsid', got '%s'", results[0].Name)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := require.New(t)
			results := cfg.Search(tt.query)
			err := tt.assert(results)
			req.NoError(err)
		})
	}
}

func TestConfig_JSON_Count(t *testing.T) {
	req := require.New(t)
	// Load the JSON file
	data, err := configIndexFile.ReadFile("config-index.json")
	req.NoError(err, "failed to read embedded config-index.json: %v", err)

	var jsonArray []ConfigParamInfo
	err = json.Unmarshal(data, &jsonArray)
	req.NoError(err, "failed to unmarshal config index JSON: %v", err)

	cfg, err := NewConfig(context.Background(), nil, true)
	req.NoError(err, "failed to create config: %v", err)
	req.Equal(len(jsonArray), len(cfg.params), "item count mismatch: json=%d, params=%d", len(jsonArray), len(cfg.params))
}

func TestConfig_Enum_JSON_Consistency(t *testing.T) {
	req := require.New(t)
	data, err := configIndexFile.ReadFile("config-index.json")
	req.NoError(err, "failed to read embedded config-index.json: %v", err)

	var jsonArray []ConfigParamInfo
	req.NoError(json.Unmarshal(data, &jsonArray), "failed to unmarshal config index JSON: %v", err)

	// --- Test 1: For every possible enum value, check there are non-empty search results in JSON ---
	// This will mean that all declared enum values exist in json

	serviceStrMap := make(map[pb.SearchConfigRequest_ServiceType]string)
	for k, v := range serviceTypeMap {
		serviceStrMap[k] = v
	}
	for enumVal, strVal := range serviceStrMap {
		found := false
		for _, param := range jsonArray {
			for _, svc := range param.Services {
				if strings.EqualFold(svc, strVal) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		req.True(found, "No JSON config param found for service enum %v (string '%s')", enumVal, strVal)
	}

	levelEnums := []pb.SearchConfigRequest_ConfigLevel{
		pb.SearchConfigRequest_BASIC,
		pb.SearchConfigRequest_ADVANCED,
		pb.SearchConfigRequest_DEV,
	}
	for _, enumVal := range levelEnums {
		levelStr := strings.ToLower(enumVal.String())
		found := false
		for _, param := range jsonArray {
			if strings.EqualFold(param.Level, levelStr) {
				found = true
				break
			}
		}
		req.True(found, "No JSON config param found for level enum %v (string '%s')", enumVal, levelStr)
	}

	// --- Test 2: For every enum value found in JSON, check it exists in Go enum ---

	jsonServiceSet := make(map[string]struct{})
	for _, param := range jsonArray {
		for _, svc := range param.Services {
			jsonServiceSet[strings.ToLower(svc)] = struct{}{}
		}
	}
	serviceStrSet := make(map[string]struct{})
	for _, v := range serviceStrMap {
		serviceStrSet[strings.ToLower(v)] = struct{}{}
	}
	for svc := range jsonServiceSet {
		req.Contains(serviceStrSet, svc, "Service '%s' found in JSON but not in Go enum", svc)
	}

	jsonLevelSet := make(map[string]struct{})
	for _, param := range jsonArray {
		if param.Level != "" {
			jsonLevelSet[strings.ToLower(param.Level)] = struct{}{}
		}
	}
	levelStrSet := make(map[string]struct{})
	for _, enumVal := range levelEnums {
		levelStrSet[strings.ToLower(enumVal.String())] = struct{}{}
	}
	for lvl := range jsonLevelSet {
		req.Contains(levelStrSet, lvl, "Level '%s' found in JSON but not in Go enum", lvl)
	}

	// Test for ParamType enum values
	typeEnums := []pb.SearchConfigRequest_ParamType{
		pb.SearchConfigRequest_STR,
		pb.SearchConfigRequest_UUID,
		pb.SearchConfigRequest_ADDR,
		pb.SearchConfigRequest_ADDRVEC,
		pb.SearchConfigRequest_BOOL,
		pb.SearchConfigRequest_INT,
		pb.SearchConfigRequest_FLOAT,
		pb.SearchConfigRequest_UINT,
		pb.SearchConfigRequest_SIZE,
		pb.SearchConfigRequest_SECS,
		pb.SearchConfigRequest_MILLISECS,
	}

	for _, enumVal := range typeEnums {
		typeStr := strings.ToLower(enumVal.String())
		found := false
		for _, param := range jsonArray {
			if strings.EqualFold(param.Type, typeStr) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("No JSON config param found for type enum %v (string '%s')", enumVal, typeStr)
		}
	}

	jsonTypeSet := make(map[string]struct{})
	for _, param := range jsonArray {
		if param.Type != "" {
			jsonTypeSet[strings.ToLower(param.Type)] = struct{}{}
		}
	}
	typeStrSet := make(map[string]struct{})
	for _, enumVal := range typeEnums {
		typeStrSet[strings.ToLower(enumVal.String())] = struct{}{}
	}
	for typ := range jsonTypeSet {
		if _, ok := typeStrSet[typ]; !ok {
			t.Errorf("Type '%s' found in JSON but not in Go enum", typ)
		}
	}
}

func TestMatchesService(t *testing.T) {
	req := require.New(t)
	osdService := pb.SearchConfigRequest_OSD
	unknownService := pb.SearchConfigRequest_ServiceType(999)

	tests := []struct {
		name     string
		info     ConfigParamInfo
		service  *pb.SearchConfigRequest_ServiceType
		expected bool
	}{
		{
			name: "nil service should match any",
			info: ConfigParamInfo{
				Services: []string{"osd"},
			},
			service:  nil,
			expected: true,
		},
		{
			name: "exact service match",
			info: ConfigParamInfo{
				Services: []string{"osd"},
			},
			service:  &osdService,
			expected: true,
		},
		{
			name: "case insensitive match",
			info: ConfigParamInfo{
				Services: []string{"OSD"},
			},
			service:  &osdService,
			expected: true,
		},
		{
			name: "no match",
			info: ConfigParamInfo{
				Services: []string{"mon"},
			},
			service:  &osdService,
			expected: false,
		},
		{
			name: "multiple services with match",
			info: ConfigParamInfo{
				Services: []string{"mon", "osd", "mgr"},
			},
			service:  &osdService,
			expected: true,
		},
		{
			name: "unknown service type",
			info: ConfigParamInfo{
				Services: []string{"custom"},
			},
			service:  &unknownService,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesService(tt.info, tt.service)
			req.Equal(tt.expected, result)
		})
	}
}

func TestMatchesName(t *testing.T) {
	req := require.New(t)
	tests := []struct {
		name     string
		info     ConfigParamInfo
		pattern  string
		expected bool
	}{
		{
			name: "empty pattern matches any",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			pattern:  "",
			expected: true,
		},
		{
			name: "exact match",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			pattern:  "osd.0",
			expected: true,
		},
		{
			name: "wildcard match prefix",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			pattern:  "osd*",
			expected: true,
		},
		{
			name: "wildcard match suffix",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			pattern:  "*.0",
			expected: true,
		},
		{
			name: "wildcard match middle",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			pattern:  "osd.*",
			expected: true,
		},
		{
			name: "no match",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			pattern:  "mon*",
			expected: false,
		},
		{
			name: "complex wildcard pattern",
			info: ConfigParamInfo{
				Name: "osd.0.cache",
			},
			pattern:  "osd.*.cache",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesName(tt.info, tt.pattern)
			req.Equal(tt.expected, result)
		})
	}
}

func TestMatchesFullText(t *testing.T) {
	req := require.New(t)
	tests := []struct {
		name       string
		info       ConfigParamInfo
		searchText string
		expected   bool
	}{
		{
			name: "empty search text matches any",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			searchText: "",
			expected:   true,
		},
		{
			name: "match in name",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			searchText: "osd",
			expected:   true,
		},
		{
			name: "match in description",
			info: ConfigParamInfo{
				Name: "osd.0",
				Desc: "OSD description",
			},
			searchText: "description",
			expected:   true,
		},
		{
			name: "match in long description",
			info: ConfigParamInfo{
				Name:     "osd.0",
				LongDesc: "Detailed OSD description",
			},
			searchText: "detailed",
			expected:   true,
		},
		{
			name: "match in tags",
			info: ConfigParamInfo{
				Name: "osd.0",
				Tags: []string{"performance", "cache"},
			},
			searchText: "cache",
			expected:   true,
		},
		{
			name: "match in services",
			info: ConfigParamInfo{
				Name:     "osd.0",
				Services: []string{"osd", "mon"},
			},
			searchText: "mon",
			expected:   true,
		},
		{
			name: "match in default value",
			info: ConfigParamInfo{
				Name:    "osd.0",
				Default: "cache_size=1G",
			},
			searchText: "cache",
			expected:   true,
		},
		{
			name: "match in daemon default",
			info: ConfigParamInfo{
				Name:          "osd.0",
				DaemonDefault: "cache_size=1G",
			},
			searchText: "cache",
			expected:   true,
		},
		{
			name: "case insensitive match",
			info: ConfigParamInfo{
				Name: "OSD.0",
			},
			searchText: "osd",
			expected:   true,
		},
		{
			name: "no match",
			info: ConfigParamInfo{
				Name: "osd.0",
			},
			searchText: "mon",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesFullText(tt.info, strings.ToLower(tt.searchText))
			req.Equal(tt.expected, result)
		})
	}
}

func TestMatchesType(t *testing.T) {
	strType := pb.SearchConfigRequest_STR
	intType := pb.SearchConfigRequest_INT
	unknownType := pb.SearchConfigRequest_ParamType(999)

	tests := []struct {
		name      string
		info      ConfigParamInfo
		paramType *pb.SearchConfigRequest_ParamType
		expected  bool
	}{
		{
			name: "nil type should match any",
			info: ConfigParamInfo{
				Type: "str",
			},
			paramType: nil,
			expected:  true,
		},
		{
			name: "matching type",
			info: ConfigParamInfo{
				Type: "str",
			},
			paramType: &strType,
			expected:  true,
		},
		{
			name: "case insensitive match",
			info: ConfigParamInfo{
				Type: "INT",
			},
			paramType: &intType,
			expected:  true,
		},
		{
			name: "non-matching type",
			info: ConfigParamInfo{
				Type: "bool",
			},
			paramType: &strType,
			expected:  false,
		},
		{
			name: "unknown enum type",
			info: ConfigParamInfo{
				Type: "unknown",
			},
			paramType: &unknownType,
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesType(tt.info, tt.paramType)
			if result != tt.expected {
				t.Errorf("matchesType() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestParseMinMax(t *testing.T) {
	req := require.New(t)
	tests := []struct {
		name string
		in   interface{}
		want *float64
	}{
		{"string zero ('0')", "0", floatPtr(0)},
		{"empty string", "", nil},
		{"float64 positive (1.1)", 1.1, floatPtr(1.1)},
		{"float64 negative (-1.1)", -1.1, floatPtr(-1.1)},
		{"string negative integer ('-1')", "-1", floatPtr(-1)},
		{"string positive integer ('1')", "1", floatPtr(1)},
		{"string positive float ('1.1')", "1.1", floatPtr(1.1)},
		{"string negative float ('-1.1')", "-1.1", floatPtr(-1.1)},
		{"nil value", nil, nil},
		{"unsupported type (struct{})", struct{}{}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMinMax(tt.in)
			if tt.want == nil {
				req.Nil(got, "expected nil, got %v", got)
			} else {
				req.NotNil(got, "expected %v, got nil", *tt.want)
				req.Equal(*tt.want, *got, "expected %v, got %v", *tt.want, *got)
			}
		})
	}
}

func floatPtr(f float64) *float64 {
	return &f
}
