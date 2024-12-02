package api

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/log"
	"github.com/clyso/ceph-api/pkg/trace"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/golang/protobuf/proto"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	otel_trace "go.opentelemetry.io/otel/trace"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

func NewGrpcServer(conf Config,
	clusterAPI pb.ClusterServer,
	usersAPI pb.UsersServer,
	authAPI pb.AuthServer,
	crushRuleAPI pb.CrushRuleServer,
	statusAPI pb.StatusServer,
	authN grpc_auth.AuthFunc,
	tracer otel_trace.TracerProvider,
	logConf log.Config) *grpc.Server {
	// lis, err := net.Listen("tcp", fmt.Sprintf(":%d", conf.GrpcPort))
	// if err != nil {
	// 	return nil, nil, err
	// }

	srv := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithTracerProvider(tracer))),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    50 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			prometheus.UnaryServerInterceptor,
			grpc_ctxtags.UnaryServerInterceptor(),
			grpc_auth.UnaryServerInterceptor(authN),
			log.UnaryInterceptor(logConf),
			trace.UnaryInterceptor(),
			ErrorInterceptor(),
			unaryServerAccessLog(conf.AccessLog),
			unaryServerRecover,
		)),
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			prometheus.StreamServerInterceptor,
			grpc_ctxtags.StreamServerInterceptor(),
			grpc_auth.StreamServerInterceptor(authN),
			log.StreamInterceptor(logConf),
			trace.StreamInterceptor(),
			ErrorStreamInterceptor(),
			streamServerAccessLog(conf.AccessLog),
			streamServerRecover,
		)))

	// Register Servers
	pb.RegisterClusterServer(srv, clusterAPI)
	pb.RegisterUsersServer(srv, usersAPI)
	pb.RegisterAuthServer(srv, authAPI)
	pb.RegisterCrushRuleServer(srv, crushRuleAPI)
	pb.RegisterStatusServer(srv, statusAPI)
	if conf.GrpcReflection {
		reflection.Register(srv)
	}
	return srv
	// return func(ctx context.Context) error {
	// 		return srv.Serve(lis)
	// 	}, func(ctx context.Context) error {
	// 		srv.Stop()
	// 		return nil
	// 	}, nil
}

func unaryServerRecover(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (_ any, err error) {
	defer func() {
		if p := recover(); p != nil {
			l := zerolog.Ctx(ctx)
			l.Error().
				Stack().
				Uint32("grpc_code", uint32(codes.Internal)).
				Interface("panic", p).Stack().Msg(string(debug.Stack()))
			err = status.Errorf(codes.Internal, "%v", p)
		}
	}()

	return handler(ctx, req)
}

func streamServerRecover(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			l := zerolog.Ctx(stream.Context())
			l.Error().
				Uint32("grpc_code", uint32(codes.Internal)).
				Interface("panic", p).Stack().Msg("panic")

			err = status.Errorf(codes.Internal, "%v", p)
		}
	}()

	return handler(srv, stream)
}

func unaryServerAccessLog(enableAccessLog bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (_ any, err error) {
		if enableAccessLog {
			zerolog.Ctx(ctx).Info().Str("grpc_method", info.FullMethod).Msg("access ceph api")
		}
		resp, err := handler(ctx, req)
		logger := zerolog.Ctx(ctx)
		rpcLogHandler(logger, err, info.FullMethod)

		return resp, err
	}
}

func rpcLogHandler(l *zerolog.Logger, err error, fullMethod string) {
	s := status.Convert(err)
	code, msg := s.Code(), s.Message()
	switch code {
	case codes.OK, codes.Canceled, codes.NotFound:
		l.Debug().Str("grpc_code", code.String()).Str("grpc_method", fullMethod).Str("response_status", "success").Msg(msg)
	case codes.Unauthenticated:
		l.Debug().Str("grpc_code", code.String()).Str("grpc_method", fullMethod).Str("response_status", "failed").Msg(msg)
	case codes.PermissionDenied, codes.FailedPrecondition, codes.AlreadyExists, codes.InvalidArgument:
		// log expected errors with WARN level
		l.Warn().Str("grpc_code", code.String()).Str("grpc_method", fullMethod).Str("response_status", "failed").Msg(msg)
	default:
		l.Error().Str("grpc_code", code.String()).Str("grpc_method", fullMethod).Str("response_status", "failed").Msg(msg)
	}
}

func streamServerAccessLog(enableAccessLog bool) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		if enableAccessLog {
			zerolog.Ctx(stream.Context()).Info().Str("grpc_method", info.FullMethod).Msg("access ceph api stream")
		}
		err = handler(srv, stream)
		logger := zerolog.Ctx(stream.Context())
		rpcLogHandler(logger, err, info.FullMethod)

		return err
	}
}

func ErrorInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		return resp, convertApiError(ctx, err)
	}
}

func ErrorStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return convertApiError(ss.Context(), handler(srv, ss))
	}
}

func convertApiError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	details := []proto.Message{&errdetails.RequestInfo{RequestId: xctx.GetTraceID(ctx)}}
	var code codes.Code
	var mappedErr error
	switch {
	case errors.Is(err, types.ErrNotImplemented):
		code = codes.Unimplemented
		mappedErr = types.ErrNotImplemented
	case errors.Is(err, types.ErrInvalidArg):
		code = codes.InvalidArgument
		mappedErr = types.ErrInvalidArg
		details = append(details, &errdetails.ErrorInfo{
			Reason: err.Error(),
		})
	case errors.Is(err, types.ErrInvalidConfig):
		code = codes.InvalidArgument
		mappedErr = types.ErrInvalidConfig
		details = append(details, &errdetails.ErrorInfo{
			Reason: err.Error(),
		})
	case errors.Is(err, types.ErrNotFound):
		code = codes.NotFound
		mappedErr = types.ErrNotFound
	case errors.Is(err, types.ErrAlreadyExists):
		code = codes.AlreadyExists
		mappedErr = types.ErrAlreadyExists
	case errors.Is(err, types.ErrUnauthenticated):
		code = codes.Unauthenticated
		mappedErr = types.ErrUnauthenticated
	case errors.Is(err, types.ErrAccessDenied):
		code = codes.PermissionDenied
		mappedErr = types.ErrAccessDenied
	default:
		code = codes.Internal
		mappedErr = types.ErrInternal
	}
	zerolog.Ctx(ctx).Err(err).Msg("api error returned")

	st := status.New(code, mappedErr.Error())
	stInfo, wdErr := st.WithDetails(details...)
	if wdErr != nil {
		zerolog.Ctx(ctx).Err(wdErr).Msg("unable to build details for grpc error message")
		return st.Err()
	}
	return stInfo.Err()
}
