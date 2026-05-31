package parity

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Route is one HTTP entry from api/http.yaml — a gRPC method selector
// bound to an HTTP method + path template.
type Route struct {
	Selector string // e.g. "ceph.Cluster.GetStatus"
	Method   string // "GET", "POST", "PUT", "DELETE", "PATCH"
	Path     string // "/api/role/{name}"
}

// EndpointID returns the canonical key used across the parity
// framework: "<METHOD> <PATH-TEMPLATE>" (e.g. "GET /api/role/{name}").
func (r Route) EndpointID() string {
	return strings.ToUpper(r.Method) + " " + r.Path
}

// LoadDashboardRoutes parses an OpenAPI 3.0 spec (e.g. the Ceph
// dashboard's openapi.yaml at
// third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml) and returns
// every (path, method) pair declared under .paths. Used to detect
// ceph-api routes that have no dashboard equivalent so the parity
// recorder can skip the dashboard side for them.
func LoadDashboardRoutes(path string) ([]Route, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var routes []Route
	for routePath, ops := range doc.Paths {
		for method := range ops {
			m := strings.ToUpper(method)
			switch m {
			case "GET", "PUT", "POST", "DELETE", "PATCH", "HEAD", "OPTIONS":
				routes = append(routes, Route{Method: m, Path: routePath})
			default:
				// "parameters", "summary", "x-*" etc. live under .paths.<route>
				// alongside method keys; skip them.
			}
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes, nil
}

// LoadHTTPRoutes parses api/http.yaml and returns every gateway rule
// as a Route. http.yaml is the source of truth for both gRPC selector
// → HTTP method+path mapping and "what endpoints we expose."
func LoadHTTPRoutes(path string) ([]Route, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Match only the fields we care about; gateway accepts custom verbs
	// but the current http.yaml only uses the standard HTTP methods.
	var doc struct {
		HTTP struct {
			Rules []struct {
				Selector string `yaml:"selector"`
				Get      string `yaml:"get"`
				Put      string `yaml:"put"`
				Post     string `yaml:"post"`
				Delete   string `yaml:"delete"`
				Patch    string `yaml:"patch"`
			} `yaml:"rules"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var routes []Route
	for i, rule := range doc.HTTP.Rules {
		methodPath := map[string]string{
			"GET":    rule.Get,
			"PUT":    rule.Put,
			"POST":   rule.Post,
			"DELETE": rule.Delete,
			"PATCH":  rule.Patch,
		}
		var found int
		for m, p := range methodPath {
			if p == "" {
				continue
			}
			found++
			routes = append(routes, Route{Selector: rule.Selector, Method: m, Path: p})
		}
		if found == 0 {
			return nil, fmt.Errorf("%s: rule #%d (%s) has no HTTP method", path, i, rule.Selector)
		}
		if found > 1 {
			return nil, fmt.Errorf("%s: rule #%d (%s) declares %d HTTP methods; one per rule", path, i, rule.Selector, found)
		}
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes, nil
}

// RouteSet indexes routes by endpoint id for O(1) membership tests.
// It also keeps a "shape" index keyed by METHOD + normalized-path
// (every {placeholder} collapsed to {}), so two routes with
// different placeholder names but the same shape — e.g. ours
// /api/role/{name} vs dashboard /api/role/{role_name} — count as
// the same route.
type RouteSet struct {
	byID    map[string]Route
	byShape map[string]Route
}

func NewRouteSet(routes []Route) *RouteSet {
	s := &RouteSet{
		byID:    make(map[string]Route, len(routes)),
		byShape: make(map[string]Route, len(routes)),
	}
	for _, r := range routes {
		s.byID[r.EndpointID()] = r
		shape := shapeID(r.Method, r.Path)
		if prev, dup := s.byShape[shape]; dup {
			panic(fmt.Sprintf(
				"parity: two routes share path shape %q: %q and %q. Path placeholders are normalized to {} for cross-spec matching; rename one placeholder so the shapes differ, or split the routes.",
				shape, prev.EndpointID(), r.EndpointID(),
			))
		}
		s.byShape[shape] = r
	}
	return s
}

// placeholderRE matches "{anything}" segments in path templates.
var placeholderRE = regexp.MustCompile(`\{[^/}]+\}`)

func shapeID(method, path string) string {
	return strings.ToUpper(method) + " " + placeholderRE.ReplaceAllString(path, "{}")
}

func (s *RouteSet) Has(endpointID string) bool {
	_, ok := s.byID[endpointID]
	return ok
}

// HasShape returns true if this set contains a route with the same
// method and path-shape (placeholder names ignored) as the given
// endpoint id. Used to ask "does the dashboard have this route?"
// when ours and dashboard might name the path parameter differently.
func (s *RouteSet) HasShape(endpointID string) bool {
	method, path, ok := strings.Cut(endpointID, " ")
	if !ok {
		return false
	}
	_, ok = s.byShape[shapeID(method, path)]
	return ok
}

func (s *RouteSet) Selectors() map[string]string {
	out := make(map[string]string, len(s.byID))
	for _, r := range s.byID {
		out[r.Selector] = r.EndpointID()
	}
	return out
}

// Closest returns up to n entries from the set whose endpoint id
// shares the longest common prefix with target. Used in error
// messages when a Call's (Method, Path) is not in http.yaml — points
// the test author at the route they probably meant.
func (s *RouteSet) Closest(target string, n int) []string {
	type scored struct {
		id    string
		score int
	}
	scoredItems := make([]scored, 0, len(s.byID))
	for id := range s.byID {
		scoredItems = append(scoredItems, scored{id: id, score: commonPrefixLen(id, target)})
	}
	sort.Slice(scoredItems, func(i, j int) bool {
		if scoredItems[i].score != scoredItems[j].score {
			return scoredItems[i].score > scoredItems[j].score
		}
		return scoredItems[i].id < scoredItems[j].id
	})
	n = min(n, len(scoredItems))
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = scoredItems[i].id
	}
	return out
}

func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// AssertRoutesCovered returns nil if every route in s (excluding
// those whose path matches any prefix in excludePathPrefixes) is
// present in covered. Otherwise it returns an error naming each
// missing endpoint and the parity-test file convention.
func AssertRoutesCovered(s *RouteSet, covered map[string]bool, excludePathPrefixes []string) error {
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var missing []string
	for _, id := range ids {
		if matchesExcluded(id, excludePathPrefixes) {
			continue
		}
		if !covered[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d HTTP route(s) declared in api/http.yaml are not covered by any parity test:\n", len(missing))
	for _, e := range missing {
		fmt.Fprintf(&b, "  - %s\n", e)
	}
	b.WriteString("add a Test_<...>_Parity to the matching test/<service>_parity_test.go that exercises the route via parity.Recorder.DoRecord")
	return fmt.Errorf("%s", b.String())
}

func matchesExcluded(endpointID string, prefixes []string) bool {
	_, path, ok := strings.Cut(endpointID, " ")
	if !ok {
		return false
	}
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
