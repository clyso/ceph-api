package cephapi

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/metadata"
)

const defaultOAuthClientID = "ceph-api"
const defaultUrl = "localhost:9969"

type ClientConfig struct {
	// GrpcUrl - Default value: "localhost:9969"
	GrpcUrl string
	// HttpUrl - Default value: "localhost:9969"
	HttpUrl string
	// OAuthClientID - vault oauth2 client id. Default value: "vault"
	OAuthClientID string
	// TLSSkipVerify set true if vault using self-issued certificates.
	TLSSkipVerify bool
	Secure        bool
	Login         string
	Password      string
}

type Client struct {
	httpURL       string
	oauthClientID string
	httpClient    *http.Client
	ts            oauth2.TokenSource
	grpcConn      *grpc.ClientConn
}

func (c *Client) Conn() *grpc.ClientConn {
	return c.grpcConn
}

func (c *Client) Close() error {
	err := c.grpcConn.Close()
	if err != nil {
		return err
	}
	tok, err := c.ts.Token()
	if err != nil {
		return err
	}
	_, err = c.httpClient.PostForm(c.httpURL+"/v1/auth/revoke", url.Values{
		"client_id":       []string{c.oauthClientID},
		"token":           []string{tok.RefreshToken},
		"token_type_hint": []string{"refresh_token"},
	})
	return err
}

// New returns authenticated vault client. Revokes all tokens on ctx cancel or on Client.Close() method.
func New(ctx context.Context, conf ClientConfig) (*Client, error) {
	c := &Client{
		httpURL:       defaultUrl,
		oauthClientID: defaultOAuthClientID,
	}
	if conf.HttpUrl != "" {
		c.httpURL = conf.HttpUrl
	}
	if conf.OAuthClientID != "" {
		c.oauthClientID = conf.OAuthClientID
	}
	grpcUrl := c.httpURL
	if conf.GrpcUrl != "" {
		grpcUrl = conf.GrpcUrl
	}
	if strings.HasPrefix(grpcUrl, "http") {
		grpcUrl = strings.TrimPrefix(grpcUrl, "https://")
		grpcUrl = strings.TrimPrefix(grpcUrl, "http://")
	}
	if !strings.HasPrefix(c.httpURL, "http") {
		if conf.Secure {
			c.httpURL = "https://" + c.httpURL
		} else {
			c.httpURL = "http://" + c.httpURL
		}
	}

	ac := oauth2.Config{
		ClientID: c.oauthClientID,
		Endpoint: oauth2.Endpoint{
			TokenURL: c.httpURL + "/api/oauth/token",
		},
	}
	c.httpClient = http.DefaultClient
	if conf.Secure && conf.TLSSkipVerify {
		customTransport := http.DefaultTransport.(*http.Transport)
		customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // honour caller opt-in to skip TLS verification
		c.httpClient = &http.Client{Transport: customTransport}
	}
	oauthCtx := context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
	token, err := ac.PasswordCredentialsToken(oauthCtx, conf.Login, conf.Password)
	if err != nil {
		return nil, err
	}
	c.ts = ac.TokenSource(ctx, token)
	if !conf.Secure {
		c.grpcConn, err = grpc.NewClient(grpcUrl,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: time.Second, Backoff: backoff.DefaultConfig}),
			grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
				t, err := ac.PasswordCredentialsToken(oauthCtx, conf.Login, conf.Password)
				if err != nil {
					return err
				}
				md := metadata.Pairs(
					"Authorization", "Bearer "+t.AccessToken,
				)
				return invoker(metadata.NewOutgoingContext(ctx, md), method, req, reply, cc, opts...)
			}),
			grpc.WithChainStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
				t, err := ac.PasswordCredentialsToken(oauthCtx, conf.Login, conf.Password)
				if err != nil {
					return nil, err
				}
				md := metadata.Pairs(
					"Authorization", "Bearer "+t.AccessToken,
				)
				return streamer(metadata.NewOutgoingContext(ctx, md), desc, cc, method, opts...)
			}),
		)
	} else {
		c.grpcConn, err = grpc.NewClient(grpcUrl,
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: conf.TLSSkipVerify})), //nolint:gosec // honour caller opt-in to skip TLS verification
			grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: time.Second, Backoff: backoff.DefaultConfig}),
			grpc.WithPerRPCCredentials(&oauth.TokenSource{TokenSource: c.ts}),
		)
	}

	if err != nil {
		return nil, err
	}
	go func() {
		<-oauthCtx.Done()
		_ = c.Close()
	}()

	return c, nil
}
