package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/pprof"
	"strconv"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/tmc/grpc-websocket-proxy/wsproxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type HTTPRoute struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

func GRPCGateway(ctx context.Context, conf Config, metricsHandler http.HandlerFunc, routes []HTTPRoute) (http.Handler, error) {
	mux := runtime.NewServeMux()
	var opts []grpc.DialOption

	if conf.Secure {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: false}))) //nolint: gosec
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	serverAddress := ":" + strconv.Itoa(conf.GrpcPort)

	// Register handlers
	err := pb.RegisterClusterHandlerFromEndpoint(ctx, mux, serverAddress, opts)
	if err != nil {
		return nil, err
	}
	err = pb.RegisterUsersHandlerFromEndpoint(ctx, mux, serverAddress, opts)
	if err != nil {
		return nil, err
	}
	err = pb.RegisterAuthHandlerFromEndpoint(ctx, mux, serverAddress, opts)
	if err != nil {
		return nil, err
	}
	err = pb.RegisterCrushRuleHandlerFromEndpoint(ctx, mux, serverAddress, opts)
	if err != nil {
		return nil, err
	}
	err = pb.RegisterStatusHandlerFromEndpoint(ctx, mux, serverAddress, opts)
	if err != nil {
		return nil, err
	}

	// Register metrics handler
	if metricsHandler != nil {
		handleGET(mux, "/metrics", metricsHandler)
	}
	for _, route := range routes {
		handleRoute(mux, route)
	}

	if conf.ServeDebug {
		handleGET(mux, "/debug/pprof/", pprof.Index)
		handleGET(mux, "/debug/pprof/cmdline", pprof.Cmdline)
		handleGET(mux, "/debug/pprof/profile", pprof.Profile)
		handleGET(mux, "/debug/pprof/symbol", pprof.Symbol)
		handleGET(mux, "/debug/pprof/trace", pprof.Trace)

		handleGET(mux, "/debug/pprof/threadcreate", pprof.Handler("threadcreate").ServeHTTP)
		handleGET(mux, "/debug/pprof/heap", pprof.Handler("heap").ServeHTTP)
		handleGET(mux, "/debug/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)
		handleGET(mux, "/debug/pprof/block", pprof.Handler("block").ServeHTTP)
		handleGET(mux, "/debug/pprof/allocs", pprof.Handler("allocs").ServeHTTP)
		handleGET(mux, "/debug/pprof/mutex", pprof.Handler("mutex").ServeHTTP)
	}

	handler := wsproxy.WebsocketProxy(mux)
	return handler, nil
	// srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", conf.HttpPort), ReadHeaderTimeout: time.Second * 5}
	// srv.Handler = handler

	// start = func(_ context.Context) error {
	// 	return srv.ListenAndServe()
	// }
	// stop = func(ctx context.Context) error {
	// 	return srv.Shutdown(ctx)
	// }
	// return
}

func handleGET(mux *runtime.ServeMux, path string, handler http.HandlerFunc) {
	handleRoute(mux, HTTPRoute{Method: http.MethodGet, Path: path, Handler: handler})
}

func handleRoute(mux *runtime.ServeMux, route HTTPRoute) {
	err := mux.HandlePath(route.Method, route.Path, func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
		route.Handler(w, r)
	})
	if err != nil {
		panic(fmt.Errorf("%w: unable to register %s http handler %s", err, route.Method, route.Path))
	}
}
