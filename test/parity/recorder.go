package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Backend identifies one side of the parity comparison.
type Backend int

const (
	Dash Backend = iota
	Ours
)

// Backends is the canonical iteration order for tests:
//
//	for _, b := range parity.Backends { r.DoRecord(b, call) }
//
// Dash runs before Ours so a CRUD flow that POST+DELETEs cleans up
// before the Ours pass starts.
var Backends = []Backend{Dash, Ours}

func (b Backend) String() string {
	switch b {
	case Dash:
		return "dashboard"
	case Ours:
		return "ceph-api"
	default:
		return fmt.Sprintf("Backend(%d)", b)
	}
}

// Call is the request shape every parity HTTP call takes. Path is the
// route template (e.g. "/api/role/{name}"), substituted at send time
// via PathParams; query string is built from QueryParams sorted by
// key for determinism. The Recorder asserts (Method, Path) matches a
// route declared in api/http.yaml.
type Call struct {
	Method      string
	Path        string
	PathParams  map[string]string
	QueryParams map[string]string
	Body        any
	Accept      string
	Headers     http.Header // optional extra headers; recorder also sets Authorization + Content-Type
}

// Package-level state, initialized once by Init.
var (
	state struct {
		mu      sync.RWMutex
		ready   bool
		dash    *Client
		ours    *Client
		routes  *RouteSet // routes from api/http.yaml (ours)
		dashRts *RouteSet // routes from dashboard openapi.yaml
		diff    map[string][]Ignore
	}

	coverageMu sync.Mutex
	coverage   = map[string]bool{}
)

// Init wires the package's two backend clients and loads
// api/http.yaml + api_diff.yaml + the dashboard openapi.yaml. Call
// once from runSetup after the parity clients have logged in.
func Init(dash, ours *Client, httpYAMLPath, dashboardSwaggerPath, apiDiffPath string) error {
	routes, err := LoadHTTPRoutes(httpYAMLPath)
	if err != nil {
		return fmt.Errorf("load http.yaml: %w", err)
	}
	dashRoutes, err := LoadDashboardRoutes(dashboardSwaggerPath)
	if err != nil {
		return fmt.Errorf("load dashboard openapi: %w", err)
	}
	diff, err := loadAPIDiff(apiDiffPath)
	if err != nil {
		return fmt.Errorf("load %s: %w", apiDiffPath, err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.dash = dash
	state.ours = ours
	state.routes = NewRouteSet(routes)
	state.dashRts = NewRouteSet(dashRoutes)
	state.diff = diff
	state.ready = true
	return nil
}

// DashboardHas returns true if the dashboard's openapi.yaml declares a
// route with the same method + path shape (placeholder names ignored).
// Used by Recorder.Backends to decide whether to fan a call out to
// both backends or only to ceph-api.
func DashboardHas(endpointID string) bool {
	state.mu.RLock()
	defer state.mu.RUnlock()
	if !state.ready {
		panic("parity: Init has not been called")
	}
	return state.dashRts.HasShape(endpointID)
}

// Routes returns the package's loaded RouteSet (panics if Init has
// not been called). Used by the TestMain coverage gates.
func Routes() *RouteSet {
	state.mu.RLock()
	defer state.mu.RUnlock()
	if !state.ready {
		panic("parity: Init has not been called")
	}
	return state.routes
}

// CoveredEndpoints returns a snapshot of every endpoint id that has
// been exercised on both backends by some parity test in this binary.
func CoveredEndpoints() map[string]bool {
	coverageMu.Lock()
	defer coverageMu.Unlock()
	out := make(map[string]bool, len(coverage))
	maps.Copy(out, coverage)
	return out
}

func markCovered(endpoint string) {
	coverageMu.Lock()
	defer coverageMu.Unlock()
	coverage[endpoint] = true
}

type record struct {
	canonical canonicalRequest
	resp      *http.Response
	body      []byte
	file      string
	line      int
}

// canonicalRequest captures the bytes-equivalent of a request so the
// recorder can detect a DoRecord(Ours, ...) that disagrees with the
// matching DoRecord(Dash, ...). Authorization, Content-Length and
// other per-backend headers are deliberately not part of the
// canonical form.
type canonicalRequest struct {
	method  string
	pathRaw string
	body    []byte
	accept  string
	headers string // sorted "k: v\n..." excluding Authorization/Content-Length
}

// Recorder is the per-test parity harness. Construct with New(t);
// the constructor registers t.Cleanup to run pairing + diff
// assertions over recorded calls when the test finishes.
type Recorder struct {
	t       testing.TB
	mu      sync.Mutex
	records map[string]map[Backend]*record
}

// New builds a Recorder bound to t, pulling clients and routes from
// the package state set by Init. Failures from cleanup assertions
// report against t via t.Errorf.
func New(t testing.TB) *Recorder {
	t.Helper()
	state.mu.RLock()
	if !state.ready {
		state.mu.RUnlock()
		t.Fatalf("parity: Init has not been called; wire it from runSetup")
	}
	state.mu.RUnlock()
	r := &Recorder{t: t, records: map[string]map[Backend]*record{}}
	t.Cleanup(r.assertAll)
	return r
}

// Backends returns the list of backends a parity test should iterate
// over for the given call. Routes that exist in api/http.yaml but not
// in the dashboard's openapi (same method + same path shape) return
// just [Ours] — there's no dashboard counterpart to compare against,
// so we still exercise our side for coverage but don't try to call
// the dashboard. Routes that exist in both return the standard
// [Dash, Ours] pair.
//
//	for _, b := range r.Backends(call) { r.DoRecord(b, call) }
func (r *Recorder) Backends(c Call) []Backend {
	endpoint := strings.ToUpper(c.Method) + " " + c.Path
	if DashboardHas(endpoint) {
		return Backends
	}
	return []Backend{Ours}
}

// Do sends call to backend b without recording it for comparison or
// coverage. Use for prep/cleanup calls (e.g. deleting leftover state).
func (r *Recorder) Do(b Backend, c Call) (*http.Response, []byte) {
	r.t.Helper()
	return r.send(b, c, false)
}

// DoRecord sends call to backend b and records the response for
// cleanup-time comparison with the other backend's record for the
// same endpoint id.
func (r *Recorder) DoRecord(b Backend, c Call) (*http.Response, []byte) {
	r.t.Helper()
	return r.send(b, c, true)
}

func (r *Recorder) send(b Backend, c Call, recordIt bool) (*http.Response, []byte) {
	r.t.Helper()

	method := strings.ToUpper(strings.TrimSpace(c.Method))
	if method == "" {
		r.t.Fatalf("parity: empty Method on Call %+v", c)
	}
	if strings.TrimSpace(c.Path) == "" {
		r.t.Fatalf("parity: empty Path on Call %+v", c)
	}

	endpoint := method + " " + c.Path
	routes := Routes()
	if !routes.Has(endpoint) {
		nearby := routes.Closest(endpoint, 3)
		r.t.Fatalf("parity: %q is not a route declared in api/http.yaml.\n"+
			"closest matches:\n  - %s\n"+
			"fix the typo in Call.Method/Path, or add a rule to api/http.yaml + run `make proto`",
			endpoint, strings.Join(nearby, "\n  - "))
	}

	pathRaw, err := materialize(c.Path, c.PathParams, c.QueryParams)
	if err != nil {
		r.t.Fatalf("parity: %v", err)
	}
	bodyBytes, err := marshalBody(c.Body)
	if err != nil {
		r.t.Fatalf("parity: marshal body for %s %s: %v", method, c.Path, err)
	}

	client := pickClient(b)
	resp, respBody, err := client.Do(context.Background(), method, pathRaw, c.Accept, bytesReader(bodyBytes), c.Headers)
	if err != nil {
		r.t.Fatalf("parity: %s %s on %s: %v", method, pathRaw, b, err)
	}

	if !recordIt {
		return resp, respBody
	}

	file, line := callerOutside()
	canon := canonicalRequest{
		method:  method,
		pathRaw: pathRaw,
		body:    bodyBytes,
		accept:  c.Accept,
		headers: canonicalHeaders(c.Headers),
	}
	rec := &record{canonical: canon, resp: resp, body: respBody, file: file, line: line}

	r.mu.Lock()
	defer r.mu.Unlock()

	byBackend, ok := r.records[endpoint]
	if !ok {
		byBackend = map[Backend]*record{}
		r.records[endpoint] = byBackend
	}
	if prev, dup := byBackend[b]; dup {
		r.t.Fatalf("parity: endpoint %q recorded twice for %s in this test\n"+
			"  first:  %s:%d\n"+
			"  second: %s:%d\n"+
			"split into two tests, or use r.Do (not r.DoRecord) for the prep/cleanup variant",
			endpoint, b, prev.file, prev.line, file, line)
	}
	if other, ok := byBackend[otherBackend(b)]; ok {
		if mismatch := canonicalDiff(other.canonical, canon); mismatch != "" {
			r.t.Fatalf("parity: %s request for %q disagrees with the prior %s request (parity requires identical requests on both sides)\n"+
				"  %s:  %s:%d\n"+
				"  %s: %s:%d\n"+
				"  diff: %s",
				b, endpoint, otherBackend(b),
				otherBackend(b), other.file, other.line,
				b, file, line,
				mismatch)
		}
	}
	byBackend[b] = rec
	return resp, respBody
}

func pickClient(b Backend) *Client {
	state.mu.RLock()
	defer state.mu.RUnlock()
	switch b {
	case Dash:
		return state.dash
	case Ours:
		return state.ours
	default:
		panic(fmt.Sprintf("parity: unknown Backend %d", b))
	}
}

func otherBackend(b Backend) Backend {
	if b == Dash {
		return Ours
	}
	return Dash
}

// assertAll runs in t.Cleanup. For every endpoint with records from
// both backends, mark coverage and diff bodies; for every endpoint
// with only one side, fail with which side is missing.
func (r *Recorder) assertAll() {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()

	state.mu.RLock()
	diff := state.diff
	state.mu.RUnlock()

	endpoints := make([]string, 0, len(r.records))
	for k := range r.records {
		endpoints = append(endpoints, k)
	}
	sort.Strings(endpoints)

	for _, endpoint := range endpoints {
		byBackend := r.records[endpoint]
		dashRec, hasDash := byBackend[Dash]
		oursRec, hasOurs := byBackend[Ours]

		switch {
		case !hasDash && !hasOurs:
			continue
		case !hasDash && !DashboardHas(endpoint):
			// ceph-api-only route: dashboard's openapi.yaml has no
			// counterpart, so r.Backends(call) returned [Ours] and the
			// test only recorded ours. Mark covered, no diff.
			markCovered(endpoint)
			continue
		case !hasDash:
			r.t.Errorf("parity: %q recorded for ceph-api only but the dashboard declares this route (use `for _, b := range r.Backends(call)` to drive both sides)\n"+
				"  ceph-api: %s:%d", endpoint, oursRec.file, oursRec.line)
			continue
		case !hasOurs:
			r.t.Errorf("parity: %q recorded for dashboard only (need a matching DoRecord(parity.Ours, ...))\n"+
				"  dashboard: %s:%d", endpoint, dashRec.file, dashRec.line)
			continue
		}

		markCovered(endpoint)

		// 415 means "Accept header is wrong"; fire the hint before
		// the generic status-class branch swallows it.
		if dashRec.resp.StatusCode == http.StatusUnsupportedMediaType {
			r.t.Errorf("parity: %q dashboard returned 415 - set Call.Accept to the versioned media type from third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml\n"+
				"  body: %s",
				endpoint, truncate(dashRec.body))
			continue
		}
		if dashRec.resp.StatusCode/100 != oursRec.resp.StatusCode/100 {
			r.t.Errorf("parity: %q status class diverges (dash=%d ours=%d)\n"+
				"  dash: %s:%d body: %s\n"+
				"  ours: %s:%d body: %s",
				endpoint,
				dashRec.resp.StatusCode, oursRec.resp.StatusCode,
				dashRec.file, dashRec.line, truncate(dashRec.body),
				oursRec.file, oursRec.line, truncate(oursRec.body))
			continue
		}

		// 204 No Content and similar empty responses are not a JSON
		// parse error; treat both-empty as equal.
		dashEmpty := len(bytes.TrimSpace(dashRec.body)) == 0
		oursEmpty := len(bytes.TrimSpace(oursRec.body)) == 0
		if dashEmpty && oursEmpty {
			continue
		}
		if dashEmpty != oursEmpty {
			r.t.Errorf("parity: %q body presence diverges (dash empty=%v ours empty=%v)\n"+
				"  dash: %s:%d body: %s\n"+
				"  ours: %s:%d body: %s",
				endpoint, dashEmpty, oursEmpty,
				dashRec.file, dashRec.line, truncate(dashRec.body),
				oursRec.file, oursRec.line, truncate(oursRec.body))
			continue
		}

		var dashJSON, oursJSON any
		if err := json.Unmarshal(dashRec.body, &dashJSON); err != nil {
			r.t.Errorf("parity: %q dashboard body not JSON: %v\n  body: %s",
				endpoint, err, truncate(dashRec.body))
			continue
		}
		if err := json.Unmarshal(oursRec.body, &oursJSON); err != nil {
			r.t.Errorf("parity: %q ceph-api body not JSON: %v\n  body: %s",
				endpoint, err, truncate(oursRec.body))
			continue
		}

		ignores := diff[endpoint]
		if diffs := Compare(dashJSON, oursJSON, ignores); len(diffs) > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "parity: %q diverges (%d divergence(s), %d declared ignore(s))\n",
				endpoint, len(diffs), len(ignores))
			fmt.Fprintf(&b, "  call: %s:%d\n", oursRec.file, oursRec.line)
			for _, d := range diffs {
				fmt.Fprintf(&b, "  - %s\n", d.String())
			}
			r.t.Errorf("%s", b.String())
		}
	}
}

// materialize substitutes {placeholders} in route with values from
// pathParams, then appends queryParams sorted by key (so the raw
// request line is byte-identical between the dash and ours calls for
// the equality check).
func materialize(route string, pathParams, queryParams map[string]string) (string, error) {
	out := route
	for k, v := range pathParams {
		needle := "{" + k + "}"
		if !strings.Contains(out, needle) {
			return "", fmt.Errorf("path_param %q has no {%s} placeholder in %s", k, k, route)
		}
		out = strings.ReplaceAll(out, needle, url.PathEscape(v))
	}
	if i := strings.Index(out, "{"); i >= 0 {
		return "", fmt.Errorf("unresolved placeholder in %s: %s", route, out[i:])
	}
	if len(queryParams) > 0 {
		keys := make([]string, 0, len(queryParams))
		for k := range queryParams {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		vals := url.Values{}
		for _, k := range keys {
			vals.Set(k, queryParams[k])
		}
		out += "?" + vals.Encode()
	}
	return out, nil
}

func marshalBody(b any) ([]byte, error) {
	if b == nil {
		return nil, nil
	}
	if raw, ok := b.([]byte); ok {
		return raw, nil
	}
	return json.Marshal(b)
}

// bytesReader returns a true-nil io.Reader for empty bodies. A
// typed-nil *bytes.Reader wrapped in an io.Reader interface trips
// http.NewRequestWithContext.
func bytesReader(b []byte) io.Reader {
	if len(b) == 0 {
		return nil
	}
	return bytes.NewReader(b)
}

// canonicalHeaders renders headers as a sorted "k: v\n" string,
// dropping per-backend headers the recorder sets itself.
func canonicalHeaders(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		if k == "Authorization" || k == "Content-Length" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h.Values(k) {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	return b.String()
}

func canonicalDiff(a, b canonicalRequest) string {
	if a.method != b.method {
		return fmt.Sprintf("method: %q vs %q", a.method, b.method)
	}
	if a.pathRaw != b.pathRaw {
		return fmt.Sprintf("path: %q vs %q", a.pathRaw, b.pathRaw)
	}
	if a.accept != b.accept {
		return fmt.Sprintf("accept: %q vs %q", a.accept, b.accept)
	}
	if a.headers != b.headers {
		return fmt.Sprintf("headers:\n--- %s\n--- %s", a.headers, b.headers)
	}
	if !bytes.Equal(a.body, b.body) {
		return fmt.Sprintf("body: %s vs %s", truncate(a.body), truncate(b.body))
	}
	return ""
}

func callerOutside() (string, int) {
	for skip := 2; skip < 16; skip++ {
		_, file, line, ok := runtime.Caller(skip)
		if !ok {
			return "?", 0
		}
		if !strings.HasSuffix(filepath.Dir(file), "/test/parity") {
			return file, line
		}
	}
	return "?", 0
}

func truncate(b []byte) string {
	const max = 512
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
