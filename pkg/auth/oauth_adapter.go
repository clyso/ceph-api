package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
)

func (s *Server) Login(ctx context.Context, username, password string) (*LoginResp, error) {
	v := url.Values{
		"grant_type": {"password"},
		"username":   {username},
		"password":   {password},
		"client_id":  {s.clientID},
	}
	req, err := http.NewRequest("POST", "http://localhost:80/api/auth", strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.clientID, "")
	resp := httptest.NewRecorder()
	s.TokenEndpoint(resp, req)
	if resp.Code < 200 || resp.Code > 299 {
		return nil, types.ErrUnauthenticated
	}
	resBody := struct {
		Token string `json:"access_token"`
	}{}
	if err := json.Unmarshal(resp.Body.Bytes(), &resBody); err != nil {
		return nil, fmt.Errorf("%w: decode auth response: %w", types.ErrInternal, err)
	}
	if resBody.Token == "" {
		return nil, fmt.Errorf("%w: unable to get token from auth response body", types.ErrInternal)
	}
	usr, err := s.userSvc.GetUser(ctx, username)
	if err != nil {
		return nil, err
	}
	return &LoginResp{
		Token:       resBody.Token,
		User:        usr,
		Permissions: s.userSvc.GetPermissions(ctx, username),
	}, nil
}

type LoginResp struct {
	Token       string
	User        user.User
	Permissions map[string][]string
}

func (s *Server) Logout(ctx context.Context) error {
	token, err := grpc_auth.AuthFromMD(ctx, "bearer")
	if err != nil {
		return err
	}
	v := url.Values{
		"client_id":       []string{s.clientID},
		"token":           []string{token},
		"token_type_hint": []string{"access_token"},
	}
	req, err := http.NewRequest("POST", "http://localhost:80/api/auth", strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()
	s.RevokeEndpoint(resp, req)
	if resp.Code < 200 || resp.Code > 299 {
		return types.ErrUnauthenticated
	}
	return nil
}
