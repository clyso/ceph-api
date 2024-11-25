package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/clyso/ceph-api/pkg/api"
	"github.com/clyso/ceph-api/pkg/auth"
	"github.com/clyso/ceph-api/pkg/config"
	"github.com/clyso/ceph-api/pkg/log"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/clyso/ceph-api/pkg/trace"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/clyso/ceph-api/pkg/user"
	"github.com/clyso/ceph-api/pkg/util"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func Start(ctx context.Context, conf config.Config, build config.Build) error {
	logger := log.GetLogger(conf.Log)
	logger.Info().
		Str("version", build.Version).
		Str("commit", build.Commit).
		Msg("app starting...")

	shutdown, tp, err := trace.NewTracerProvider(ctx, conf.Trace, build.Version)
	if err != nil {
		return err
	}
	defer shutdown(context.Background())

	radosSvc, err := rados.New(conf.Rados)
	if err != nil {
		return err
	}
	defer radosSvc.Close()

	clusterAPI := api.NewClusterAPI(radosSvc)
	userSvc, err := user.New(radosSvc)
	if err != nil {
		return err
	}
	if conf.App.CreateAdmin {
		_, err = userSvc.GetUser(ctx, conf.App.AdminUsername)
		if errors.Is(err, types.ErrNotFound) {
			err = userSvc.CreateUser(ctx, user.User{
				Username: conf.App.AdminUsername,
				Roles:    []string{"administrator"},
				Password: conf.App.AdminPassword,
				Name:     util.StrPtr("ceph api default administrator"),
				Enabled:  true,
			})
			if err != nil {
				return fmt.Errorf("%w: unable to create admin user", err)
			}
		} else if err == nil {
			err = userSvc.UpdateUser(ctx, user.User{
				Username: conf.App.AdminUsername,
				Roles:    []string{"administrator"},
				Password: conf.App.AdminPassword,
				Name:     util.StrPtr("ceph api default administrator"),
				Enabled:  true,
			})
			if err != nil {
				return fmt.Errorf("%w: unable to update admin user", err)
			}
		} else {
			logger.Info().Err(err).Msg("skip default administrator creation")
		}
	}
	usersAPI := api.NewUsersAPI(userSvc)

	authServer, err := auth.NewServer(conf.Auth, userSvc)
	if err != nil {
		return err
	}
	authAPI := api.NewAuthAPI(authServer)

	server := util.NewServer()

	crushRuleAPI := api.NewCrushRuleAPI(radosSvc)

	authChecker := auth.AuthFunc(userSvc, authServer.Provider(), authServer.GetPublicKey)
	grpcServer := api.NewGrpcServer(conf.Api, clusterAPI, usersAPI, authAPI, crushRuleAPI, authChecker, tp, conf.Log)

	var metricsHandler http.HandlerFunc
	if conf.Metrics.Enabled {
		metricsHandler = promhttp.Handler().ServeHTTP
	}
	oauthHandlers := map[string]http.HandlerFunc{
		"/api/oauth/token":      authServer.TokenEndpoint,
		"/api/oauth/auth":       authServer.AuthEndpoint,
		"/api/oauth/revoke":     authServer.RevokeEndpoint,
		"/api/oauth/introspect": authServer.IntrospectionEndpoint,
	}
	httpServer, err := api.GRPCGateway(ctx, conf.Api, metricsHandler, oauthHandlers)
	if err != nil {
		return err
	}
	start, stop, err := api.Serve(ctx, conf.Api, grpcServer, httpServer)
	if err != nil {
		return err
	}
	err = server.Add("api", start, stop)
	if err != nil {
		return err
	}

	return server.Start(ctx)
}
