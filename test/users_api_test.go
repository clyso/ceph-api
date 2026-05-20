//go:build cgo

package test

import (
	"context"
	"testing"
	"time"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func Test_Users_CRUD(t *testing.T) {
	var (
		username = "test-user"
		pwd      = "test-pass"
		email    = "email"
		name     = "billy harrington"
		roles    = []string{"cluster-manager", "read-only"}
	)
	r := require.New(t)
	client := pb.NewUsersClient(admConn)

	_, err := client.GetUser(tstCtx, &pb.GetUserReq{Username: username})
	r.Error(err, "user %s should not exist", username)

	req := &pb.CreateUserReq{
		Email:             &email,
		Enabled:           true,
		Name:              &name,
		Password:          pwd,
		PwdUpdateRequired: false,
		Roles:             roles,
		Username:          username,
	}
	_, err = client.CreateUser(tstCtx, req)
	r.NoError(err)
	t.Cleanup(func() {
		client.DeleteUser(context.Background(), &pb.GetUserReq{Username: username})
	})

	usr, err := client.GetUser(tstCtx, &pb.GetUserReq{Username: username})
	r.NoError(err)
	r.EqualValues(username, usr.Username)
	r.ElementsMatch(roles, usr.Roles)
	r.NotNil(usr.Email)
	r.EqualValues(email, *usr.Email)
	r.NotNil(usr.Name)
	r.EqualValues(name, *usr.Name)
	r.WithinDuration(time.Now(), usr.LastUpdate.AsTime(), 5*time.Second)
	r.True(usr.Enabled)

	var fromList *pb.User
	res, err := client.ListUsers(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	for i, u := range res.Users {
		if u.Username == username {
			fromList = res.Users[i]
			break
		}
	}
	r.NotNil(fromList)
	r.True(proto.Equal(usr, fromList))

	var (
		newEmal = "new-email"
		newName = "new name"
	)
	req.Email = &newEmal
	req.Name = &newName
	req.Enabled = false
	req.Roles = nil
	_, err = client.UpdateUser(tstCtx, req)
	r.NoError(err)

	usrUpd, err := client.GetUser(tstCtx, &pb.GetUserReq{Username: username})
	r.NoError(err)
	r.EqualValues(username, usrUpd.Username)
	r.Empty(usrUpd.Roles)
	r.NotNil(usrUpd.Email)
	r.EqualValues(newEmal, *usrUpd.Email)
	r.NotNil(usrUpd.Name)
	r.EqualValues(newName, *usrUpd.Name)
	r.WithinDuration(time.Now(), usrUpd.LastUpdate.AsTime(), 5*time.Second)
	r.False(usrUpd.Enabled)

	fromList = nil
	res, err = client.ListUsers(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	for i, u := range res.Users {
		if u.Username == username {
			fromList = res.Users[i]
			break
		}
	}
	r.NotNil(fromList)
	r.True(proto.Equal(usrUpd, fromList))

	_, err = client.DeleteUser(tstCtx, &pb.GetUserReq{Username: username})
	r.NoError(err)
	_, err = client.GetUser(tstCtx, &pb.GetUserReq{Username: username})
	r.Error(err)
}

func Test_Roles_CRUD(t *testing.T) {
	r := require.New(t)
	client := pb.NewUsersClient(admConn)

	var (
		name        = "test-role"
		descr       = "test role descr"
		permissions = map[string]*structpb.ListValue{
			"rgw": {
				Values: []*structpb.Value{structpb.NewStringValue("read")},
			},
			"hosts": {Values: []*structpb.Value{
				structpb.NewStringValue("create"),
				structpb.NewStringValue("delete"),
			}},
		}
	)
	_, err := client.GetRole(tstCtx, &pb.GetRoleReq{Name: name})
	r.Error(err)

	roleReq := &pb.Role{
		Name:              name,
		Description:       &descr,
		ScopesPermissions: permissions,
	}
	_, err = client.CreateRole(tstCtx, roleReq)
	r.NoError(err)
	t.Cleanup(func() {
		client.DeleteRole(tstCtx, &pb.GetRoleReq{Name: name})
	})

	role, err := client.GetRole(tstCtx, &pb.GetRoleReq{Name: name})
	r.NoError(err)
	r.True(proto.Equal(roleReq, role))

	roles, err := client.ListRoles(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	var fromList *pb.Role
	for _, v := range roles.Roles {
		if v.Name == name {
			fromList = v
			break
		}
	}
	r.NotNil(fromList)
	r.True(proto.Equal(role, fromList))

	updDesc := "bla bla"
	updRole := &pb.Role{
		Name:        name,
		Description: &updDesc,
		ScopesPermissions: map[string]*structpb.ListValue{
			"rgw": {Values: []*structpb.Value{structpb.NewStringValue("read")}},
			"hosts": {Values: []*structpb.Value{
				structpb.NewStringValue("create"),
				structpb.NewStringValue("delete"),
			}},
		},
	}
	_, err = client.UpdateRole(tstCtx, updRole)
	r.NoError(err)

	role, err = client.GetRole(tstCtx, &pb.GetRoleReq{Name: name})
	r.NoError(err)
	r.True(proto.Equal(role, updRole))

	roles, err = client.ListRoles(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	fromList = nil
	for _, v := range roles.Roles {
		if v.Name == name {
			fromList = v
			break
		}
	}
	r.NotNil(fromList)
	r.True(proto.Equal(fromList, updRole))

	_, err = client.DeleteRole(tstCtx, &pb.GetRoleReq{Name: name})
	r.NoError(err)

	_, err = client.GetRole(tstCtx, &pb.GetRoleReq{Name: name})
	r.Error(err)
}
