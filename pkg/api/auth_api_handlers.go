package api

import (
	"context"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/auth"
	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/types"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func NewAuthAPI(svc *auth.Server) pb.AuthServer {
	return &authAPI{
		svc: svc,
	}
}

type authAPI struct {
	svc *auth.Server
}

func (a *authAPI) Check(ctx context.Context, req *pb.TokenCheckReq) (*pb.TokenCheckResp, error) {
	return nil, types.ErrNotImplemented
}

func (a *authAPI) Login(ctx context.Context, req *pb.LoginReq) (*pb.LoginResp, error) {
	res, err := a.svc.Login(ctx, req.Username, req.Password)
	if err != nil {
		return nil, err
	}
	return &pb.LoginResp{
		Token:             res.Token,
		Username:          res.User.Username,
		PwdUpdateRequired: res.User.PwdUpdateRequired,
		PwdExpirationDate: tsToPb(res.User.PwdExpirationDate),
		Sso:               false,
		Permissions:       permissionsToPB(res.Permissions),
	}, nil
}

func (a *authAPI) Logout(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	err := a.svc.Logout(ctx)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (a *authAPI) Whoami(ctx context.Context, _ *emptypb.Empty) (*pb.WhoamiResp, error) {
	username := xctx.GetUsername(ctx)
	if username == "" {
		return nil, types.ErrUnauthenticated
	}

	return &pb.WhoamiResp{
		Subject:     username,
		AuthType:    xctx.GetAuthType(ctx),
		ApiKeyId:    xctx.GetAPIKeyID(ctx),
		Roles:       xctx.GetRoles(ctx),
		Permissions: permissionsToPB(xctx.GetPermissions(ctx)),
	}, nil
}

func permissionsToPB(in map[string][]string) map[string]*structpb.ListValue {
	permissions := make(map[string]*structpb.ListValue, len(in))
	for p, vals := range in {
		permissions[p] = &structpb.ListValue{}
		for _, v := range vals {
			permissions[p].Values = append(permissions[p].Values, structpb.NewStringValue(v))
		}
	}
	return permissions
}
