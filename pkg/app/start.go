package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/clyso/ceph-api/pkg/api"
	"github.com/clyso/ceph-api/pkg/auth"
	"github.com/clyso/ceph-api/pkg/cephconfig"
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
	// Determine the connection type message
	connectionType := ""
	if IsMock {
		connectionType = " with MOCK Ceph connection"
	}
	logger.Info().
		Str("version", build.Version).
		Str("commit", build.Commit).
		Msg("app starting" + connectionType)

	shutdown, tp, err := trace.NewTracerProvider(ctx, conf.Trace, build.Version)
	if err != nil {
		return err
	}
	defer shutdown(context.Background())

	// get rados connection
	radosConn, err := getRadosConnection(conf.Rados)

	if err != nil {
		return err
	}

	radosSvc, err := rados.New(radosConn)

	if err != nil {
		return err
	}
	defer radosSvc.Close()

	configSvc, err := cephconfig.NewConfig(ctx, radosSvc, false)
	if err != nil {
		return err
	}

	clusterAPI := api.NewClusterAPI(radosSvc, configSvc)
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

	statusAPI := api.NewStatusAPI(radosSvc)

	authChecker := auth.AuthFunc(userSvc, authServer.Provider(), authServer.GetPublicKey)
	grpcServer := api.NewGrpcServer(conf.Api, clusterAPI, usersAPI, authAPI, crushRuleAPI, statusAPI, authChecker, tp, conf.Log)

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
