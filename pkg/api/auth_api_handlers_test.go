package api

import (
	"context"
	"errors"
	"testing"

	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/types"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestAuthAPIWhoami(t *testing.T) {
	ctx := context.Background()
	ctx = xctx.SetUsername(ctx, "admin")
	ctx = xctx.SetRoles(ctx, []string{"administrator"})
	ctx = xctx.SetPermissions(ctx, map[string][]string{"pool": {"read", "create"}})
	ctx = xctx.SetAuthType(ctx, "jwt")
	ctx = xctx.SetAPIKeyID(ctx, "")

	resp, err := (&authAPI{}).Whoami(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("Whoami() error = %v", err)
	}
	if resp.Subject != "admin" {
		t.Fatalf("subject = %q, want admin", resp.Subject)
	}
	if resp.AuthType != "jwt" {
		t.Fatalf("auth_type = %q, want jwt", resp.AuthType)
	}
	if len(resp.Roles) != 1 || resp.Roles[0] != "administrator" {
		t.Fatalf("roles = %v, want [administrator]", resp.Roles)
	}
	poolPerms := resp.Permissions["pool"].GetValues()
	if len(poolPerms) != 2 || poolPerms[0].GetStringValue() != "read" || poolPerms[1].GetStringValue() != "create" {
		t.Fatalf("pool permissions = %v, want [read create]", poolPerms)
	}
}

func TestAuthAPIWhoamiRequiresUsername(t *testing.T) {
	_, err := (&authAPI{}).Whoami(context.Background(), &emptypb.Empty{})
	if !errors.Is(err, types.ErrUnauthenticated) {
		t.Fatalf("Whoami() error = %v, want %v", err, types.ErrUnauthenticated)
	}
}
