package api

import (
	"context"
	"time"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/clyso/ceph-api/pkg/auth"
	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/types"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func (a *authAPI) CreateAPIKey(ctx context.Context, req *pb.CreateAPIKeyReq) (*pb.CreateAPIKeyResp, error) {
	var expiresAt *time.Time
	if req.GetExpiresAt() != nil {
		expires := req.GetExpiresAt().AsTime()
		expiresAt = &expires
	}
	rec, token, err := a.svc.CreateAPIKey(ctx, auth.CreateAPIKeyRequest{
		Name:        req.GetName(),
		Description: req.GetDescription(),
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return nil, err
	}
	return &pb.CreateAPIKeyResp{Key: apiKeyToPB(rec), Token: token}, nil
}

func (a *authAPI) ListAPIKeys(ctx context.Context, _ *emptypb.Empty) (*pb.ListAPIKeysResp, error) {
	keys, err := a.svc.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	res := &pb.ListAPIKeysResp{Keys: make([]*pb.APIKeyResp, 0, len(keys))}
	for _, key := range keys {
		res.Keys = append(res.Keys, apiKeyToPB(key))
	}
	return res, nil
}

func (a *authAPI) GetAPIKey(ctx context.Context, req *pb.GetAPIKeyReq) (*pb.APIKeyResp, error) {
	rec, err := a.svc.GetAPIKey(ctx, req.GetKeyId())
	if err != nil {
		return nil, err
	}
	return apiKeyToPB(rec), nil
}

func (a *authAPI) RevokeAPIKey(ctx context.Context, req *pb.RevokeAPIKeyReq) (*emptypb.Empty, error) {
	if err := a.svc.RevokeAPIKey(ctx, req.GetKeyId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func apiKeyToPB(in auth.APIKeyRecord) *pb.APIKeyResp {
	return &pb.APIKeyResp{
		Id:          in.ID,
		Name:        in.Name,
		Description: in.Description,
		ClusterId:   in.ClusterID,
		Enabled:     in.Enabled,
		RevokedAt:   timeToPB(in.RevokedAt),
		CreatedAt:   timestamppb.New(in.CreatedAt),
		CreatedBy:   in.CreatedBy,
		ExpiresAt:   timeToPB(in.ExpiresAt),
		LastUsedAt:  timeToPB(in.LastUsedAt),
	}
}

func timeToPB(in *time.Time) *timestamppb.Timestamp {
	if in == nil {
		return nil
	}
	return timestamppb.New(*in)
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
