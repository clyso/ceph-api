package parity

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// loadAPIDiff parses api_diff.yaml. Keys are endpoint ids
// ("<METHOD> <PATH-TEMPLATE>"); values are lists of Ignore. Both
// path and reason are required. An empty or missing file is OK (no
// declared divergences).
func loadAPIDiff(path string) (map[string][]Ignore, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]Ignore{}, nil
		}
		return nil, err
	}
	out := map[string][]Ignore{}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	for endpoint, ignores := range out {
		for i, ig := range ignores {
			if strings.TrimSpace(ig.Reason) == "" {
				return nil, fmt.Errorf("%s ignore #%d (%s): reason: is required", endpoint, i, ig.Path)
			}
			if strings.TrimSpace(ig.Path) == "" {
				return nil, fmt.Errorf("%s ignore #%d: path: is required", endpoint, i)
			}
		}
	}
	return out, nil
}
