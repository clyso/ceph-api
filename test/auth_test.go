//go:build cgo

package test

import (
	"context"
	"crypto/tls"
	"net/http"
	"testing"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

func Test_Auth_grpc_API(t *testing.T) {
	r := require.New(t)
	client := pb.NewAuthClient(grpcConn)
	clusterClient := pb.NewClusterClient(grpcConn)

	_, err := clusterClient.GetStatus(tstCtx, &emptypb.Empty{})
	r.Error(err)

	res, err := client.Login(tstCtx, &pb.LoginReq{
		Username: admin,
		Password: pass,
	})
	r.NoError(err)
	r.EqualValues(admin, res.Username)
	r.NotEmpty(res.Permissions)

	md := metadata.Pairs("Authorization", "Bearer "+res.Token)
	authCtx := metadata.NewOutgoingContext(context.Background(), md)

	_, err = clusterClient.GetStatus(authCtx, &emptypb.Empty{})
	r.NoError(err)

	_, err = clusterClient.GetStatus(authCtx, &emptypb.Empty{})
	r.NoError(err)

	_, err = client.Logout(authCtx, &emptypb.Empty{})
	r.NoError(err)

	_, err = clusterClient.GetStatus(authCtx, &emptypb.Empty{})
	r.Error(err)

}
func Test_Auth_Oauth_API(t *testing.T) {
	r := require.New(t)
	clusterClient := pb.NewClusterClient(grpcConn)

	_, err := clusterClient.GetStatus(tstCtx, &emptypb.Empty{})
	r.Error(err)
	ctx, _, err := authenticateGrpcOauth(conf.App.AdminUsername, conf.App.AdminPassword)
	r.NoError(err)
	_, err = clusterClient.GetStatus(ctx, &emptypb.Empty{})
	r.NoError(err)

}

func authenticateGrpcOauth(login, pass string) (context.Context, *oauth2.Token, error) {
	c := oauth2.Config{
		ClientID: conf.Auth.ClientID,
		Endpoint: oauth2.Endpoint{
			TokenURL: httpAddr + "/api/oauth/token",
		},
	}
	ctx := context.Background()
	if conf.Api.Secure {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}})
	}
	token, err := c.PasswordCredentialsToken(ctx, login, pass)
	if err != nil {
		return nil, nil, err
	}
	md := metadata.Pairs(
		"Authorization", "Bearer "+token.AccessToken,
	)
	return metadata.NewOutgoingContext(context.Background(), md), token, nil
}
