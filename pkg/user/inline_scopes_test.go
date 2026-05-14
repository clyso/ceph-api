package user

import "testing"

func TestNormalizeInlineScopes(t *testing.T) {
	got, err := NormalizeInlineScopes([]string{"config-opt:update/read", "monitor:read", "config-opt:read"})
	if err != nil {
		t.Fatalf("NormalizeInlineScopes() error = %v", err)
	}
	want := []string{"config-opt:read", "config-opt:update", "monitor:read"}
	if len(got) != len(want) {
		t.Fatalf("scopes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scopes = %v, want %v", got, want)
		}
	}
}

func TestNormalizeInlineScopesRejectsInvalidScopes(t *testing.T) {
	for _, scopes := range [][]string{
		{"config-opt"},
		{"unknown:read"},
		{"config-opt:admin"},
		{"config-opt:read/"},
	} {
		t.Run(scopes[0], func(t *testing.T) {
			if _, err := NormalizeInlineScopes(scopes); err == nil {
				t.Fatal("NormalizeInlineScopes() succeeded, want error")
			}
		})
	}
}
