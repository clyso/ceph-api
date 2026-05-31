package parity

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// timestampSkewTolerance bounds the wall-clock difference between the two
// recorded RFC3339-vs-unix-seconds samples. The recorder hits dashboard
// then ours sequentially, so a CRUD response's `lastUpdate` can disagree
// by the inter-call gap. Generous on slow CI.
const timestampSkewTolerance = 5 * time.Second

// Ignore declares a JSONPath that the diff walker should skip, with a
// mandatory reason so the divergence catalogue stays audit-readable.
type Ignore struct {
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
}

// Diff describes a single divergence between dashboard ("expected") and
// ceph-api ("actual") response bodies at a JSONPath location.
type Diff struct {
	Path     string
	Kind     string
	Expected any
	Actual   any
}

func (d Diff) String() string {
	return fmt.Sprintf("%s @ %s: expected=%v actual=%v", d.Kind, d.Path, render(d.Expected), render(d.Actual))
}

// Compare walks expected (dashboard) and actual (ceph-api) JSON trees
// (decoded from json.Unmarshal into any) and reports every divergence not
// covered by an ignore. Ignore paths use a tiny subset of JSONPath:
// "$" root, "." for descent, and "*" wildcard for "any array index or map
// key" at a single position.
func Compare(expected, actual any, ignores []Ignore) []Diff {
	patterns := make([][]string, 0, len(ignores))
	for _, ig := range ignores {
		segs, err := parsePath(ig.Path)
		if err != nil {
			// Loader validates non-empty; treat bad syntax as an internal
			// programming error rather than silently mis-comparing.
			panic(fmt.Errorf("parity: invalid ignore path %q: %w", ig.Path, err))
		}
		patterns = append(patterns, segs)
	}
	return walk(nil, expected, actual, patterns)
}

func walk(path []string, expected, actual any, patterns [][]string) []Diff {
	if matchesAny(path, patterns) {
		return nil
	}

	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return typeDiff(path, expected, actual)
		}
		return walkMap(path, e, a, patterns)
	case []any:
		a, ok := actual.([]any)
		if !ok {
			return typeDiff(path, expected, actual)
		}
		return walkArray(path, e, a, patterns)
	default:
		// Well-known proto-vs-dashboard JSON-shape divergences are suppressed
		// inline rather than per-endpoint:
		//   - google.protobuf.Timestamp ↔ unix-seconds integer
		//   - protojson int64-as-string ↔ JSON integer
		//   - protojson Timestamp "Z" form ↔ Ceph's "+0000" RFC3339 spelling
		// Keeps api_diff.yaml from being polluted with the same boilerplate
		// every time we expose a typed proto field. See CLAUDE.md.
		if matched, applied := coerceEqual(expected, actual); applied {
			if matched {
				return nil
			}
			return []Diff{{Path: pathStr(path), Kind: "value", Expected: expected, Actual: actual}}
		}
		if reflect.TypeOf(expected) != reflect.TypeOf(actual) {
			return typeDiff(path, expected, actual)
		}
		if !scalarEqual(expected, actual) {
			return []Diff{{Path: pathStr(path), Kind: "value", Expected: expected, Actual: actual}}
		}
		return nil
	}
}

// coerceEqual lets the diff matcher treat protojson's idiomatic scalar
// encodings as equivalent to the dashboard's plain-JSON encoding:
//   - RFC3339 timestamp string ↔ unix-seconds JSON number (matches when
//     the wall-clock difference is within timestampSkewTolerance).
//   - int64-as-JSON-string ↔ JSON number (matches when the integer values
//     are equal).
//
// `applied` is true when one side was a string the function recognised as
// either form; only then is `matched` meaningful. If both sides have the
// same kind, or the string isn't recognised, returns (false, false) and
// the walker falls through to its normal type-diff behaviour.
func coerceEqual(a, b any) (matched, applied bool) {
	aStr, aIsStr := a.(string)
	bStr, bIsStr := b.(string)
	if aIsStr && bIsStr {
		// Both RFC3339 strings: the dashboard emits Ceph's UTC offset form
		// ("...+0000", microsecond precision) while protojson renders a
		// google.protobuf.Timestamp with a "Z" suffix. Same instant, different
		// spelling — match within the same skew tolerance as the string↔unix case.
		at, aOK := parseCephTime(aStr)
		bt, bOK := parseCephTime(bStr)
		if aOK && bOK {
			skew := at.Sub(bt)
			if skew < 0 {
				skew = -skew
			}
			return skew <= timestampSkewTolerance, true
		}
		return false, false
	}
	if aIsStr == bIsStr {
		return false, false
	}
	str := aStr
	other := b
	if bIsStr {
		str = bStr
		other = a
	}
	num, ok := other.(float64)
	if !ok {
		return false, false
	}
	if t, ok := parseCephTime(str); ok {
		skew := time.Duration(int64(num)-t.Unix()) * time.Second
		if skew < 0 {
			skew = -skew
		}
		return skew <= timestampSkewTolerance, true
	}
	if i, err := strconv.ParseInt(str, 10, 64); err == nil {
		return float64(i) == num, true
	}
	return false, false
}

// parseCephTime accepts both protojson's RFC3339 ("...Z" / "+00:00") and
// Ceph's numeric-offset spelling without a colon ("...+0000").
func parseCephTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func walkMap(path []string, expected, actual map[string]any, patterns [][]string) []Diff {
	keys := unionKeys(expected, actual)
	var diffs []Diff
	for _, k := range keys {
		sub := appendSeg(path, k)
		ev, eok := expected[k]
		av, aok := actual[k]
		switch {
		case eok && aok:
			diffs = append(diffs, walk(sub, ev, av, patterns)...)
		case !aok:
			// protojson with EmitUnpopulated emits unset proto3 optional
			// fields as null; the dashboard's hand-rolled JSON usually
			// omits them. Treat null on one side and absent on the other
			// as equivalent so the matcher doesn't false-positive on that.
			if ev == nil {
				continue
			}
			if matchesAny(sub, patterns) {
				continue
			}
			diffs = append(diffs, Diff{Path: pathStr(sub), Kind: "missing", Expected: ev})
		case !eok:
			if av == nil {
				continue
			}
			if matchesAny(sub, patterns) {
				continue
			}
			diffs = append(diffs, Diff{Path: pathStr(sub), Kind: "extra", Actual: av})
		}
	}
	return diffs
}

func walkArray(path []string, expected, actual []any, patterns [][]string) []Diff {
	if len(expected) != len(actual) {
		return []Diff{{
			Path:     pathStr(path),
			Kind:     "length",
			Expected: len(expected),
			Actual:   len(actual),
		}}
	}
	var diffs []Diff
	for i := range expected {
		sub := appendSeg(path, strconv.Itoa(i))
		diffs = append(diffs, walk(sub, expected[i], actual[i], patterns)...)
	}
	return diffs
}

// appendSeg returns path with seg appended, always allocating a fresh
// underlying array so sibling recursion in the walker can't stomp on each
// other's path stack.
func appendSeg(path []string, seg string) []string {
	out := make([]string, len(path)+1)
	copy(out, path)
	out[len(path)] = seg
	return out
}

func typeDiff(path []string, expected, actual any) []Diff {
	return []Diff{{
		Path:     pathStr(path),
		Kind:     "type",
		Expected: fmt.Sprintf("%T", expected),
		Actual:   fmt.Sprintf("%T", actual),
	}}
}

func scalarEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a == b
}

func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parsePath(p string) ([]string, error) {
	if p == "" {
		return nil, fmt.Errorf("empty path")
	}
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("must start with $")
	}
	rest := strings.TrimPrefix(p, "$")
	if rest == "" {
		return nil, nil
	}
	if !strings.HasPrefix(rest, ".") {
		return nil, fmt.Errorf("expected . after $, got %q", rest)
	}
	segs := strings.Split(rest[1:], ".")
	if slices.Contains(segs, "") {
		return nil, fmt.Errorf("empty segment in %q", p)
	}
	return segs, nil
}

func matchesAny(path []string, patterns [][]string) bool {
	for _, pat := range patterns {
		if matches(path, pat) {
			return true
		}
	}
	return false
}

func matches(path, pattern []string) bool {
	if len(path) != len(pattern) {
		return false
	}
	for i := range path {
		if pattern[i] == "*" {
			continue
		}
		if pattern[i] != path[i] {
			return false
		}
	}
	return true
}

func pathStr(p []string) string {
	if len(p) == 0 {
		return "$"
	}
	return "$." + strings.Join(p, ".")
}

func render(v any) string {
	switch x := v.(type) {
	case nil:
		return "<nil>"
	case string:
		return strconv.Quote(x)
	default:
		return fmt.Sprintf("%v", x)
	}
}
