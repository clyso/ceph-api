package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthAPIKeyCreateCommand(t *testing.T) {
	var gotAuth string
	var gotReq struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		ExpiresAt   string   `json:"expires_at"`
		Scopes      []string `json:"scopes"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != apiKeyCreatePath {
			t.Fatalf("path = %s, want %s", r.URL.Path, apiKeyCreatePath)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"capi_v1_ak_test.secret","key":{"id":"ak_test"}}`))
	}))
	defer server.Close()

	cmd := newRootCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{
		"auth", "api-key", "create",
		"--endpoint", server.URL + "/",
		"--token", "jwt-token",
		"--name", "poc",
		"--description", "business logic",
		"--expires-at", "2027-05-13T00:00:00Z",
		"--scope", "config-opt:read",
		"--scope", "config-opt:update",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotAuth != "Bearer jwt-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotReq.Name != "poc" || gotReq.Description != "business logic" || gotReq.ExpiresAt != "2027-05-13T00:00:00Z" {
		t.Fatalf("request = %+v", gotReq)
	}
	if len(gotReq.Scopes) != 2 || gotReq.Scopes[0] != "config-opt:read" || gotReq.Scopes[1] != "config-opt:update" {
		t.Fatalf("scopes = %v, want config-opt read/update", gotReq.Scopes)
	}
	if got, want := out.String(), "capi_v1_ak_test.secret\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestAuthAPIKeyCreateCommandReadsTokenFromStdin(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"capi_v1_ak_test.secret"}`))
	}))
	defer server.Close()

	cmd := newRootCmd()
	cmd.SetIn(bytes.NewBufferString("stdin-token\n"))
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"auth", "api-key", "create",
		"--endpoint", server.URL,
		"--token", "-",
		"--name", "poc",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotAuth != "Bearer stdin-token" {
		t.Fatalf("Authorization = %q, want stdin token", gotAuth)
	}
}

func TestAuthAPIKeyCreateCommandRequiresToken(t *testing.T) {
	t.Setenv("CEPH_API_TOKEN", "")
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"auth", "api-key", "create", "--name", "poc"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute() succeeded, want token error")
	}
}
