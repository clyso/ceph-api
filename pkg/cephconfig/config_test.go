package cephconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
)

func TestConfig_Search_FilteringAndSorting(t *testing.T) {
	ctx := context.Background()
	cfg, err := NewConfig(ctx, nil, true) // skipUpdate: true for testing
	if err != nil {
		t.Fatalf("failed to create config: %v", err)
	}

	sortName := pb.SearchConfigRequest_SortField(pb.SearchConfigRequest_NAME)
	sortType := pb.SearchConfigRequest_SortField(pb.SearchConfigRequest_TYPE)
	sortLevel := pb.SearchConfigRequest_SortField(pb.SearchConfigRequest_LEVEL)
	sortAsc := pb.SearchConfigRequest_SortOrder(pb.SearchConfigRequest_ASC)
	sortDesc := pb.SearchConfigRequest_SortOrder(pb.SearchConfigRequest_DESC)
	serviceOsd := pb.SearchConfigRequest_ServiceType(pb.SearchConfigRequest_OSD)
	levelBasic := pb.SearchConfigRequest_ConfigLevel(pb.SearchConfigRequest_BASIC)

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := cfg.Search(tt.query)
			if err := tt.assert(results); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestConfig_JSON_Count(t *testing.T) {
	// Load the JSON file
	data, err := configIndexFile.ReadFile("config-index.json")
	if err != nil {
		t.Fatalf("failed to read embedded config-index.json: %v", err)
	}

	var jsonArray []ConfigParamInfo
	if err := json.Unmarshal(data, &jsonArray); err != nil {
		t.Fatalf("failed to unmarshal config index JSON: %v", err)
	}

	cfg, err := NewConfig(context.Background(), nil, true)
	if err != nil {
		t.Fatalf("failed to create config: %v", err)
	}
	if len(jsonArray) != len(cfg.params) {
		t.Errorf("item count mismatch: json=%d, params=%d", len(jsonArray), len(cfg.params))
	}
}
