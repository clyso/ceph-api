package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const apiKeyCreatePath = "/api/v1/auth/api-keys"

type authAPIKeyCreateOptions struct {
	endpoint    string
	token       string
	name        string
	description string
	expiresAt   string
	output      string
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate and manage credentials",
	}
	cmd.AddCommand(newAuthAPIKeyCmd())
	return cmd
}

func newAuthAPIKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api-key",
		Short: "Manage API keys",
	}
	cmd.AddCommand(newAuthAPIKeyCreateCmd())
	return cmd
}

func newAuthAPIKeyCreateCmd() *cobra.Command {
	opts := &authAPIKeyCreateOptions{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthAPIKeyCreate(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", "http://localhost:9969", "ceph-api base URL")
	cmd.Flags().StringVar(&opts.token, "token", "", "administrator JWT; use '-' to read from stdin, or CEPH_API_TOKEN")
	cmd.Flags().StringVar(&opts.name, "name", "", "API key name")
	cmd.Flags().StringVar(&opts.description, "description", "", "API key description")
	cmd.Flags().StringVar(&opts.expiresAt, "expires-at", "", "API key expiration as RFC3339 timestamp")
	cmd.Flags().StringVarP(&opts.output, "output", "o", "token", "output format: token or json")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func runAuthAPIKeyCreate(ctx context.Context, stdin io.Reader, stdout io.Writer, opts *authAPIKeyCreateOptions) error {
	token, err := resolveToken(stdin, opts.token)
	if err != nil {
		return err
	}
	endpoint := normalizeEndpoint(opts.endpoint)
	if endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	if opts.output != "token" && opts.output != "json" {
		return fmt.Errorf("unsupported output format %q", opts.output)
	}

	body := map[string]any{
		"name": opts.name,
	}
	if opts.description != "" {
		body["description"] = opts.description
	}
	if opts.expiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, opts.expiresAt)
		if err != nil {
			return fmt.Errorf("parse --expires-at as RFC3339: %w", err)
		}
		body["expires_at"] = expiresAt.Format(time.RFC3339)
	}

	reqBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+apiKeyCreatePath, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("create API key: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var createResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &createResp); err != nil {
		return fmt.Errorf("decode create API key response: %w", err)
	}
	if createResp.Token == "" {
		return fmt.Errorf("create API key response did not include token")
	}
	if opts.output == "json" {
		_, err = fmt.Fprintln(stdout, string(respBody))
		return err
	}
	_, err = fmt.Fprintln(stdout, createResp.Token)
	return err
}

func resolveToken(stdin io.Reader, flagToken string) (string, error) {
	token := strings.TrimSpace(flagToken)
	if token == "-" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		token = strings.TrimSpace(string(raw))
	} else if token == "" {
		token = strings.TrimSpace(os.Getenv("CEPH_API_TOKEN"))
	}
	if token == "" {
		return "", fmt.Errorf("token is required via --token, --token -, or CEPH_API_TOKEN")
	}
	return token, nil
}

func normalizeEndpoint(endpoint string) string {
	return strings.TrimRight(strings.TrimSpace(endpoint), "/")
}
