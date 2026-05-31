package parity

import (
	"encoding/json"
	"strings"
	"testing"
)

func jsonAny(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("unmarshal %q: %v", s, err)
	}
	return v
}

func TestCompare_EqualScalar(t *testing.T) {
	if d := Compare(jsonAny(t, `1`), jsonAny(t, `1`), nil); len(d) != 0 {
		t.Errorf("expected no diff, got %v", d)
	}
}

func TestCompare_ScalarDiffers(t *testing.T) {
	d := Compare(jsonAny(t, `1`), jsonAny(t, `2`), nil)
	if len(d) != 1 || d[0].Kind != "value" {
		t.Fatalf("expected one value diff, got %v", d)
	}
}

func TestCompare_TypeMismatch(t *testing.T) {
	// Pick a string that doesn't parse as int or RFC3339 so the matcher
	// can't coerce it to the numeric side.
	d := Compare(jsonAny(t, `1`), jsonAny(t, `"hello"`), nil)
	if len(d) != 1 || d[0].Kind != "type" {
		t.Fatalf("expected type diff, got %v", d)
	}
}

func TestCompare_MissingAndExtraKeys(t *testing.T) {
	exp := jsonAny(t, `{"a":1,"b":2}`)
	act := jsonAny(t, `{"b":2,"c":3}`)
	d := Compare(exp, act, nil)
	kinds := map[string]int{}
	for _, x := range d {
		kinds[x.Kind]++
	}
	if kinds["missing"] != 1 || kinds["extra"] != 1 {
		t.Fatalf("want 1 missing + 1 extra, got %v (%v)", kinds, d)
	}
}

func TestCompare_TimestampStringEqualsUnixSeconds(t *testing.T) {
	// protojson emits Timestamp as RFC3339; dashboard emits the same instant
	// as a unix-seconds integer. Matcher should treat them as equivalent.
	exp := jsonAny(t, `{"lastUpdate": 1700000000}`)
	act := jsonAny(t, `{"lastUpdate": "2023-11-14T22:13:20Z"}`)
	if d := Compare(exp, act, nil); len(d) != 0 {
		t.Fatalf("expected RFC3339-vs-unix coercion to match, got %v", d)
	}
	// Within the skew tolerance — CRUD endpoints record sequentially so the
	// two timestamps can disagree by the inter-call gap.
	exp = jsonAny(t, `{"lastUpdate": 1700000000}`)
	act = jsonAny(t, `{"lastUpdate": "2023-11-14T22:13:23Z"}`)
	if d := Compare(exp, act, nil); len(d) != 0 {
		t.Fatalf("expected 3-second skew to be within tolerance, got %v", d)
	}
	// Outside the tolerance → real value diff.
	exp = jsonAny(t, `{"lastUpdate": 1700000000}`)
	act = jsonAny(t, `{"lastUpdate": "2023-11-14T22:15:00Z"}`)
	d := Compare(exp, act, nil)
	if len(d) != 1 || d[0].Kind != "value" {
		t.Fatalf("expected one value diff outside tolerance, got %v", d)
	}
}

func TestCompare_TimestampStringFormatsEqual(t *testing.T) {
	// The dashboard emits Ceph's RFC3339 UTC-offset form with microseconds;
	// protojson renders the same Timestamp instant with a "Z" suffix.
	exp := jsonAny(t, `{"create_time": "2026-05-31T11:30:15.842225+0000"}`)
	act := jsonAny(t, `{"create_time": "2026-05-31T11:30:15.842225Z"}`)
	if d := Compare(exp, act, nil); len(d) != 0 {
		t.Fatalf("expected RFC3339 offset-vs-Z forms to match, got %v", d)
	}
	// Different instants beyond tolerance must still diff.
	exp = jsonAny(t, `{"create_time": "2026-05-31T11:30:15+0000"}`)
	act = jsonAny(t, `{"create_time": "2026-05-31T12:30:15Z"}`)
	d := Compare(exp, act, nil)
	if len(d) != 1 || d[0].Kind != "value" {
		t.Fatalf("expected one value diff on differing instants, got %v", d)
	}
	// Two non-timestamp strings still compare literally (no false coercion).
	exp = jsonAny(t, `{"pool_name": "a"}`)
	act = jsonAny(t, `{"pool_name": "b"}`)
	d = Compare(exp, act, nil)
	if len(d) != 1 || d[0].Kind != "value" {
		t.Fatalf("expected one value diff on differing strings, got %v", d)
	}
}

func TestCompare_Int64AsStringEqualsInt(t *testing.T) {
	// protojson encodes int64 as a JSON string; if a future endpoint mixes
	// int64 with a dashboard plain-number response, the matcher should treat
	// them as equivalent so the proto stays well-typed.
	exp := jsonAny(t, `{"rule_id": 7}`)
	act := jsonAny(t, `{"rule_id": "7"}`)
	if d := Compare(exp, act, nil); len(d) != 0 {
		t.Fatalf("expected int-vs-int64-string coercion to match, got %v", d)
	}
	// Different integer values: matcher must still report a diff.
	exp = jsonAny(t, `{"rule_id": 7}`)
	act = jsonAny(t, `{"rule_id": "8"}`)
	d := Compare(exp, act, nil)
	if len(d) != 1 || d[0].Kind != "value" {
		t.Fatalf("expected one value diff on differing ints, got %v", d)
	}
}

func TestCompare_NullEqualsAbsent(t *testing.T) {
	if d := Compare(jsonAny(t, `{"a":1}`), jsonAny(t, `{"a":1,"b":null}`), nil); len(d) != 0 {
		t.Errorf("null on actual should not diff vs absent on expected, got %v", d)
	}
	if d := Compare(jsonAny(t, `{"a":1,"b":null}`), jsonAny(t, `{"a":1}`), nil); len(d) != 0 {
		t.Errorf("null on expected should not diff vs absent on actual, got %v", d)
	}
}

func TestCompare_IgnoreScalarPath(t *testing.T) {
	exp := jsonAny(t, `{"uptime": 312, "fsid": "abc"}`)
	act := jsonAny(t, `{"uptime": 87,  "fsid": "abc"}`)
	d := Compare(exp, act, []Ignore{{Path: "$.uptime", Reason: "live"}})
	if len(d) != 0 {
		t.Fatalf("expected no diff after ignore, got %v", d)
	}
}

func TestCompare_IgnoreMissingKey(t *testing.T) {
	exp := jsonAny(t, `{"a":1,"b":2}`)
	act := jsonAny(t, `{"a":1}`)
	d := Compare(exp, act, []Ignore{{Path: "$.b", Reason: "drop"}})
	if len(d) != 0 {
		t.Fatalf("ignored missing key should not diff, got %v", d)
	}
}

func TestCompare_IgnoreSubtree(t *testing.T) {
	exp := jsonAny(t, `{"health": {"status": "OK", "checks": {"x": {"severity": "low"}}}}`)
	act := jsonAny(t, `{"health": {"status": "WARN", "checks": {"y": {"severity": "high"}}}}`)
	d := Compare(exp, act, []Ignore{{Path: "$.health", Reason: "live"}})
	if len(d) != 0 {
		t.Fatalf("subtree ignore should suppress all diffs, got %v", d)
	}
}

func TestCompare_WildcardArray(t *testing.T) {
	exp := jsonAny(t, `{"mons": [{"name":"a","nonce":1},{"name":"b","nonce":2}]}`)
	act := jsonAny(t, `{"mons": [{"name":"a","nonce":99},{"name":"b","nonce":100}]}`)
	d := Compare(exp, act, []Ignore{{Path: "$.mons.*.nonce", Reason: "per-run"}})
	if len(d) != 0 {
		t.Fatalf("wildcard ignore should suppress array-element field, got %v", d)
	}
}

func TestCompare_WildcardMap(t *testing.T) {
	exp := jsonAny(t, `{"checks": {"foo": {"muted_until": 1}, "bar": {"muted_until": 2}}}`)
	act := jsonAny(t, `{"checks": {"foo": {"muted_until": 9}, "bar": {"muted_until": 8}}}`)
	d := Compare(exp, act, []Ignore{{Path: "$.checks.*.muted_until", Reason: "live"}})
	if len(d) != 0 {
		t.Fatalf("wildcard map ignore should match any key, got %v", d)
	}
}

func TestCompare_ArrayLengthMismatch(t *testing.T) {
	exp := jsonAny(t, `[1,2,3]`)
	act := jsonAny(t, `[1,2]`)
	d := Compare(exp, act, nil)
	if len(d) != 1 || d[0].Kind != "length" {
		t.Fatalf("expected one length diff, got %v", d)
	}
}

func TestCompare_NestedArrayDiff(t *testing.T) {
	exp := jsonAny(t, `[{"v":1},{"v":2}]`)
	act := jsonAny(t, `[{"v":1},{"v":3}]`)
	d := Compare(exp, act, nil)
	if len(d) != 1 || d[0].Path != "$.1.v" {
		t.Fatalf("expected one diff at $.1.v, got %v", d)
	}
}

func TestCompare_DeepIgnorePath(t *testing.T) {
	exp := jsonAny(t, `{"a": {"b": {"c": 1}}}`)
	act := jsonAny(t, `{"a": {"b": {"c": 2}}}`)
	d := Compare(exp, act, []Ignore{{Path: "$.a.b.c", Reason: "live"}})
	if len(d) != 0 {
		t.Fatalf("deep ignore should match, got %v", d)
	}
}

func TestParsePath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{"$", nil, false},
		{"$.foo", []string{"foo"}, false},
		{"$.a.b.c", []string{"a", "b", "c"}, false},
		{"$.checks.*.muted_until", []string{"checks", "*", "muted_until"}, false},
		{"", nil, true},
		{"foo", nil, true},
		{"$foo", nil, true},
		{"$.foo..bar", nil, true},
	}
	for _, tc := range cases {
		got, err := parsePath(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("parsePath(%q) err=%v want err=%v", tc.in, err, tc.err)
			continue
		}
		if !tc.err && strings.Join(got, ".") != strings.Join(tc.want, ".") {
			t.Errorf("parsePath(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCompare_BadIgnorePathPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on bad ignore path")
		}
	}()
	Compare(1.0, 1.0, []Ignore{{Path: "bogus", Reason: "x"}})
}
