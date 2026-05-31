package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	probeUsername = "parity-probe-noperm"
	probePassword = "parity-probe-noperm-pass"
)

// change_password rejects with AccessDenied for an identity mismatch
// (ctx username != path username) before HasPermissions runs, so its
// 403 doesn't tell us whether the permission gate is wired.
var probeExclusions = map[string]struct{}{
	"POST /api/user/{username}/change_password": {},
}

func isProbeExcluded(c Call) bool {
	if strings.HasPrefix(c.Path, "/api/auth") {
		return true
	}
	_, ok := probeExclusions[strings.ToUpper(c.Method)+" "+c.Path]
	return ok
}

// POST on both backends keeps the user-list GET bytewise equal between
// them; login on Ours because the sweep only hits Ours.
func bootstrapProbeClients(ctx context.Context, dash, ours *Client, accept string) (noPerm, noAuth *Client, err error) {
	body := map[string]any{
		"username":          probeUsername,
		"password":          probePassword,
		"name":              "parity probe (no perms)",
		"email":             "",
		"roles":             []string{},
		"enabled":           true,
		"pwdUpdateRequired": false,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal probe user body: %w", err)
	}

	for _, admin := range []*Client{dash, ours} {
		resp, respBody, doErr := admin.Do(ctx, http.MethodPost, "/api/user", accept, bytes.NewReader(raw), nil)
		if doErr != nil {
			return nil, nil, fmt.Errorf("create probe user on %s: %w", admin.BaseURL, doErr)
		}
		if resp.StatusCode == http.StatusConflict {
			continue
		}
		if resp.StatusCode/100 != 2 {
			return nil, nil, fmt.Errorf("create probe user on %s: status %d: %s", admin.BaseURL, resp.StatusCode, respBody)
		}
	}

	noPerm, err = Login(ctx, ours.BaseURL, ours.HTTP, accept, probeUsername, probePassword)
	if err != nil {
		return nil, nil, fmt.Errorf("login probe user: %w", err)
	}
	noAuth = &Client{BaseURL: ours.BaseURL, HTTP: ours.HTTP}
	return noPerm, noAuth, nil
}

func (r *Recorder) runAuthzProbes(c Call) {
	r.t.Helper()
	if isProbeExcluded(c) {
		return
	}
	state.mu.RLock()
	noAuth := state.noAuth
	noPerm := state.noPerm
	state.mu.RUnlock()
	r.assertNoAuthProbe(c, noAuth)
	r.assertNoPermProbe(c, noPerm)
}

func (r *Recorder) assertNoAuthProbe(c Call, client *Client) {
	r.t.Helper()
	resp, body := r.send(Ours, c, false, client)
	endpoint := strings.ToUpper(c.Method) + " " + c.Path
	if resp.StatusCode != http.StatusUnauthorized {
		r.t.Errorf("parity authz: %s no-auth probe expected HTTP 401, got %d\n  body: %s",
			endpoint, resp.StatusCode, truncate(body))
		return
	}
	parsed, perr := parseGrpcError(body)
	if perr != nil {
		r.t.Errorf("parity authz: %s no-auth probe body not a grpc error: %v\n  body: %s",
			endpoint, perr, truncate(body))
		return
	}
	if parsed.Code != codeUnauthenticated {
		r.t.Errorf("parity authz: %s no-auth probe code=%d, want %d (codes.Unauthenticated)\n  body: %s",
			endpoint, parsed.Code, codeUnauthenticated, truncate(body))
	}
	if !detailsHaveReason(parsed.Details, "ErrUnauthenticated") {
		r.t.Errorf("parity authz: %s no-auth probe missing details[*].reason=%q\n  body: %s",
			endpoint, "ErrUnauthenticated", truncate(body))
	}
}

func (r *Recorder) assertNoPermProbe(c Call, client *Client) {
	r.t.Helper()
	resp, body := r.send(Ours, c, false, client)
	endpoint := strings.ToUpper(c.Method) + " " + c.Path
	if resp.StatusCode != http.StatusForbidden {
		r.t.Errorf("parity authz: %s no-perm probe expected HTTP 403, got %d\n  body: %s",
			endpoint, resp.StatusCode, truncate(body))
		return
	}
	parsed, perr := parseGrpcError(body)
	if perr != nil {
		r.t.Errorf("parity authz: %s no-perm probe body not a grpc error: %v\n  body: %s",
			endpoint, perr, truncate(body))
		return
	}
	if parsed.Code != codePermissionDenied {
		r.t.Errorf("parity authz: %s no-perm probe code=%d, want %d (codes.PermissionDenied)\n  body: %s",
			endpoint, parsed.Code, codePermissionDenied, truncate(body))
	}
	if parsed.Message != "AccessDenied" {
		r.t.Errorf("parity authz: %s no-perm probe message=%q, want %q\n  body: %s",
			endpoint, parsed.Message, "AccessDenied", truncate(body))
	}
}

// Restated to keep parity off the grpc import graph.
const (
	codeUnauthenticated  = 16
	codePermissionDenied = 7
)

type grpcErrorBody struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Details []json.RawMessage `json:"details"`
}

func parseGrpcError(body []byte) (grpcErrorBody, error) {
	var out grpcErrorBody
	if err := json.Unmarshal(body, &out); err != nil {
		return grpcErrorBody{}, err
	}
	return out, nil
}

func detailsHaveReason(details []json.RawMessage, reason string) bool {
	for _, d := range details {
		var obj struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(d, &obj); err != nil {
			continue
		}
		if obj.Reason == reason {
			return true
		}
	}
	return false
}
