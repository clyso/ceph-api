package api

import (
	"context"
	"sort"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func NewUsersAPI(svc *user.Service) pb.UsersServer {
	return &usersAPI{
		svc: svc,
	}
}

type usersAPI struct {
	svc *user.Service
}

func (u *usersAPI) CloneRole(ctx context.Context, req *pb.CloneRoleReq) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermRead, user.PermCreate); err != nil {
		return nil, err
	}
	err := u.svc.CloneRole(ctx, req.Name, req.NewName)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (u *usersAPI) CreateRole(ctx context.Context, req *pb.Role) (*pb.Role, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermCreate); err != nil {
		return nil, err
	}
	if err := u.svc.CreateRole(ctx, roleFromPb(req)); err != nil {
		return nil, err
	}
	role, err := u.svc.GetRole(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	return roleToPb(role), nil
}

func roleFromPb(r *pb.Role) user.Role {
	permissions := make(map[string][]string, len(r.ScopesPermissions))
	for k, v := range r.ScopesPermissions {
		for _, p := range v.Values {
			permissions[k] = append(permissions[k], p.GetStringValue())
		}
		// Dashboard's Role.set_scope_permissions sorts permissions alphabetically
		// before storing, so the GET round-trip emits them in sorted order. Mirror
		// that here to keep the wire shape identical.
		sort.Strings(permissions[k])
	}

	return user.Role{
		Name:        r.Name,
		Description: r.Description,
		IsSystem:    false,
		Permissions: permissions,
	}
}

func (u *usersAPI) DeleteRole(ctx context.Context, req *pb.GetRoleReq) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermDelete); err != nil {
		return nil, err
	}
	err := u.svc.DeleteRole(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (u *usersAPI) GetRole(ctx context.Context, req *pb.GetRoleReq) (*pb.Role, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermRead); err != nil {
		return nil, err
	}
	role, err := u.svc.GetRole(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	return roleToPb(role), nil
}

func roleToPb(r user.Role) *pb.Role {
	permissions := make(map[string]*structpb.ListValue, len(r.Permissions))
	for p, vals := range r.Permissions {
		permissions[p] = &structpb.ListValue{}
		for _, v := range vals {
			permissions[p].Values = append(permissions[p].Values, structpb.NewStringValue(v))
		}
	}
	return &pb.Role{
		Name:              r.Name,
		Description:       r.Description,
		ScopesPermissions: permissions,
		System:            r.IsSystem,
	}
}

func (u *usersAPI) ListRoles(ctx context.Context, _ *emptypb.Empty) (*pb.RolesResp, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermRead); err != nil {
		return nil, err
	}
	roles, err := u.svc.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	res := make([]*pb.Role, len(roles))
	for i, r := range roles {
		res[i] = roleToPb(r)
	}
	return &pb.RolesResp{Roles: res}, nil
}

func (u *usersAPI) UpdateRole(ctx context.Context, req *pb.Role) (*pb.Role, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermUpdate); err != nil {
		return nil, err
	}
	if err := u.svc.UpdateRole(ctx, roleFromPb(req)); err != nil {
		return nil, err
	}
	role, err := u.svc.GetRole(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	return roleToPb(role), nil
}

func (u *usersAPI) UserChangePassword(ctx context.Context, req *pb.UserChangePasswordReq) (*emptypb.Empty, error) {
	if xctx.GetUsername(ctx) != req.Username {
		return nil, types.ErrAccessDenied
	}
	err := u.svc.ChangePassword(ctx, req.Username, req.OldPassword, req.NewPassword)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (u *usersAPI) CreateUser(ctx context.Context, req *pb.CreateUserReq) (*pb.User, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermCreate); err != nil {
		return nil, err
	}
	usr := user.User{
		Username:          req.Username,
		Roles:             req.Roles,
		Password:          req.Password,
		Name:              req.Name,
		Email:             req.Email,
		Enabled:           req.Enabled,
		PwdUpdateRequired: req.PwdUpdateRequired,
	}
	if req.PwdExpirationDate != nil {
		expIn := int(req.PwdExpirationDate.Seconds)
		usr.PwdExpirationDate = &expIn
	}
	if err := u.svc.CreateUser(ctx, usr); err != nil {
		return nil, err
	}
	created, err := u.svc.GetUser(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	return userToPb(created), nil
}

func (u *usersAPI) DeleteUser(ctx context.Context, req *pb.GetUserReq) (*emptypb.Empty, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermDelete); err != nil {
		return nil, err
	}
	err := u.svc.DeleteUser(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (u *usersAPI) GetUser(ctx context.Context, req *pb.GetUserReq) (*pb.User, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermRead); err != nil {
		return nil, err
	}
	usr, err := u.svc.GetUser(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	return userToPb(usr), nil
}

func userToPb(usr user.User) *pb.User {
	res := &pb.User{
		Email:             usr.Email,
		Enabled:           usr.Enabled,
		Name:              usr.Name,
		LastUpdate:        &timestamppb.Timestamp{Seconds: int64(usr.LastUpdate)},
		PwdUpdateRequired: usr.PwdUpdateRequired,
		Roles:             usr.Roles,
		Username:          usr.Username,
	}
	if usr.PwdExpirationDate != nil {
		res.PwdExpirationDate = &timestamppb.Timestamp{Seconds: int64(*usr.PwdExpirationDate)}
	}
	return res
}

func (u *usersAPI) ListUsers(ctx context.Context, _ *emptypb.Empty) (*pb.UsersResp, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermRead); err != nil {
		return nil, err
	}
	users, err := u.svc.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	res := make([]*pb.User, len(users))
	for i, usr := range users {
		res[i] = userToPb(usr)
	}
	return &pb.UsersResp{Users: res}, nil
}

func (u *usersAPI) UpdateUser(ctx context.Context, req *pb.CreateUserReq) (*pb.User, error) {
	if err := user.HasPermissions(ctx, user.ScopeUser, user.PermUpdate); err != nil {
		return nil, err
	}
	usr := user.User{
		Username:          req.Username,
		Roles:             req.Roles,
		Password:          req.Password,
		Name:              req.Name,
		Email:             req.Email,
		Enabled:           req.Enabled,
		PwdUpdateRequired: req.PwdUpdateRequired,
	}
	if req.PwdExpirationDate != nil {
		expIn := int(req.PwdExpirationDate.Seconds)
		usr.PwdExpirationDate = &expIn
	}
	if err := u.svc.UpdateUser(ctx, usr); err != nil {
		return nil, err
	}
	updated, err := u.svc.GetUser(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	return userToPb(updated), nil
}
