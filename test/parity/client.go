package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client is a thin authenticated HTTP client targeting a single ceph-api or
// ceph-dashboard backend. Both backends accept the same POST /api/auth
// {"username","password"} -> {"token"} login shape (the dashboard's flow,
// which ceph-api mirrors at /api/auth for drop-in compatibility), so a
// single Client implementation covers both.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Token   string
}

// Login posts credentials to baseURL+/api/auth using the provided http.Client
// (the caller controls transport, e.g. tls.InsecureSkipVerify for the
// dashboard's self-signed cert) and returns a ready-to-use Client.
//
// accept is the versioned media type the dashboard's /api/auth requires
// (currently "application/vnd.ceph.api.v1.0+json"); pass the empty string for
// backends that accept plain application/json. The same Accept value is sent
// here and on every subsequent call via Client.Do is not bound to this value
// (each parity entry declares its own Accept).
func Login(ctx context.Context, baseURL string, hc *http.Client, accept, user, pass string) (*Client, error) {
	body, err := json.Marshal(map[string]string{"username": user, "password": pass})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(baseURL, "/api/auth"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("login %s: status %d: %s", baseURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("login %s: parse response: %w (body=%s)", baseURL, err, raw)
	}
	if out.Token == "" {
		return nil, fmt.Errorf("login %s: empty token in response: %s", baseURL, raw)
	}
	return &Client{BaseURL: baseURL, HTTP: hc, Token: out.Token}, nil
}

// Do sends method+path against the client's base URL with the bearer token
// attached. Body is sent verbatim; pass nil for GET/DELETE. accept is the
// versioned media type the dashboard requires for this endpoint, sent
// verbatim on both backends so requests stay clones; pass "" to omit.
// extra is applied on top (the recorder uses it for arbitrary user-set
// headers; Authorization and Content-Type are always managed here).
func (c *Client) Do(ctx context.Context, method, path, accept string, body io.Reader, extra http.Header) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, joinURL(c.BaseURL, path), body)
	if err != nil {
		return nil, nil, err
	}
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("read body: %w", err)
	}
	return resp, raw, nil
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}
