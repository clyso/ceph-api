//go:build cgo

package test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	cephapi "github.com/clyso/ceph-api"
	"github.com/clyso/ceph-api/pkg/app"
	"github.com/clyso/ceph-api/pkg/config"
	"github.com/clyso/ceph-api/test/parity"
	"github.com/clyso/ceph-api/test/testenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	conf     config.Config
	grpcAddr string
	httpAddr string
	tstCtx   context.Context
	grpcConn *grpc.ClientConn
	admConn  *grpc.ClientConn

	cephEnv *testenv.CephEnv

	// Authenticated HTTP clients used by the dashboard-parity test. Both
	// log in once at TestMain time via POST /api/auth and re-use the bearer
	// token; parity tests assume auth works and do not re-verify it.
	parityOurs *parity.Client
	parityDash *parity.Client
)

const (
	admin = "ceph-e2e-test-admin"
	pass  = "ceph-e2e-test-pass"

	// Paths relative to the test package directory (cwd when
	// `go test ./test/...` runs inside the Docker container).
	parityHTTPYAMLPath  = "../api/http.yaml"
	parityDashboardYAML = "../third_party/ceph/src/pybind/mgr/dashboard/openapi.yaml"
	parityAPIDiffPath   = "parity/api_diff.yaml"
)

func runSetup(m *testing.M) (int, error) {
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer bootCancel()

	env, err := testenv.NewCephEnv(bootCtx)
	if err != nil {
		return 1, fmt.Errorf("start ceph env: %w", err)
	}
	cephEnv = env
	defer cephEnv.Close()

	confDir, err := env.CephConfig(bootCtx)
	if err != nil {
		return 1, fmt.Errorf("extract ceph config: %w", err)
	}
	defer os.RemoveAll(confDir)

	// librados reads CEPH_CONF before falling back to /etc/ceph/ceph.conf.
	if err := os.Setenv("CEPH_CONF", confDir+"/ceph.conf"); err != nil {
		return 1, fmt.Errorf("set CEPH_CONF: %w", err)
	}

	if err := config.Get(&conf); err != nil {
		return 1, fmt.Errorf("load config: %w", err)
	}
	conf.Log.Json = false
	conf.Api.Secure = false
	port, _ := getRandomPort()
	conf.Api.GrpcPort = port
	conf.Api.HttpPort = port
	conf.App.CreateAdmin = true
	conf.App.AdminUsername = admin
	conf.App.AdminPassword = pass
	conf.App.BcryptPwdCost = 4

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tstCtx = ctx

	appReady := make(chan error, 1)
	go func() {
		appCtx, cancelFn := context.WithCancel(ctx)
		defer cancelFn()
		appReady <- app.Start(appCtx, conf, config.Build{Version: "test"})
	}()

	grpcAddr = fmt.Sprintf("localhost:%d", conf.Api.GrpcPort)
	if conf.Api.Secure {
		httpAddr = fmt.Sprintf("https://localhost:%d", conf.Api.HttpPort)
	} else {
		httpAddr = fmt.Sprintf("http://localhost:%d", conf.Api.HttpPort)
	}
	fmt.Println("http", httpAddr)
	fmt.Println("grpc", grpcAddr)

	if err := waitForTCP(ctx, grpcAddr, 2*time.Minute); err != nil {
		return 1, fmt.Errorf("wait for api server: %w", err)
	}

	tlsOpt := grpc.WithTransportCredentials(insecure.NewCredentials())
	if conf.Api.Secure {
		tlsOpt = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}))
	}
	grpcConn, err = grpc.NewClient(grpcAddr,
		tlsOpt,
		grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoff.DefaultConfig, MinConnectTimeout: time.Second}),
	)
	if err != nil {
		return 1, fmt.Errorf("dial grpc: %w", err)
	}

	c, err := cephapi.New(tstCtx, cephapi.ClientConfig{
		GrpcUrl:  grpcAddr,
		HttpUrl:  httpAddr,
		Login:    admin,
		Password: pass,
	})
	if err != nil {
		return 1, fmt.Errorf("authenticate admin: %w", err)
	}
	admConn = c.Conn()

	// Dashboard caches its user table at startup and doesn't observe writes
	// ceph-api made to mgr/dashboard/accessdb_v2 (e.g. the bootstrap admin
	// above). Reload it so the parity user-list pass sees the same set on
	// both backends.
	if err := cephEnv.ReloadDashboard(bootCtx); err != nil {
		return 1, fmt.Errorf("reload dashboard: %w", err)
	}

	// Dashboard /api/auth requires the v1.0 versioned media type; ceph-api
	// tolerates any Accept (gateway registered under MIMEWildcard) so we
	// send the same header to both sides to keep requests cloned.
	const loginAccept = "application/vnd.ceph.api.v1.0+json"

	parityOurs, err = parity.Login(tstCtx, httpAddr, &http.Client{}, loginAccept, admin, pass)
	if err != nil {
		return 1, fmt.Errorf("authenticate ceph-api parity client: %w", err)
	}

	dashURL, dashUser, dashPass, err := cephEnv.Dashboard(tstCtx)
	if err != nil {
		return 1, fmt.Errorf("dashboard URL: %w", err)
	}
	dashHTTP := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // dashboard uses self-signed cert
	}}
	parityDash, err = parity.Login(tstCtx, dashURL, dashHTTP, loginAccept, dashUser, dashPass)
	if err != nil {
		return 1, fmt.Errorf("authenticate dashboard parity client: %w", err)
	}

	if err := parity.Init(parityDash, parityOurs, parityHTTPYAMLPath, parityDashboardYAML, parityAPIDiffPath); err != nil {
		return 1, fmt.Errorf("parity.Init: %w", err)
	}

	exitCode := m.Run()

	// Coverage gate 1: every gRPC method declared in proto descriptors
	// must have a rule in api/http.yaml. Forces every new RPC to be
	// HTTP-exposed. Standard infrastructure services (gRPC reflection,
	// gRPC health checking, OTLP collector) are not part of the
	// ceph-api surface and are excluded by service-prefix.
	if err := parity.AssertGRPCMethodsRouted(parity.Routes(), []string{
		"grpc.", "opentelemetry.",
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if exitCode == 0 {
			exitCode = 1
		}
	}

	// Coverage gate 2: every HTTP route in api/http.yaml must have been
	// exercised on some parity test. /api/auth* is excluded - the
	// bootstrap login above already covers it and parity clients
	// can't dogfood their own auth flow.
	if err := parity.AssertRoutesCovered(
		parity.Routes(),
		parity.CoveredEndpoints(),
		[]string{"/api/auth"},
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if exitCode == 0 {
			exitCode = 1
		}
	}

	cancel()
	grpcConn.Close()
	c.Close()

	select {
	case startErr := <-appReady:
		if startErr != nil && startErr != context.Canceled {
			fmt.Fprintln(os.Stderr, "app.Start exited:", startErr)
		}
	case <-time.After(5 * time.Second):
	}
	return exitCode, nil
}

func waitForTCP(ctx context.Context, addr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var dialer net.Dialer
	for {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s not reachable: %w", addr, err)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func getRandomPort() (int, string) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(err)
	}
	addr := l.Addr().String()
	addrs := strings.Split(addr, ":")
	if err := l.Close(); err != nil {
		panic(err)
	}
	port, err := strconv.Atoi(addrs[len(addrs)-1])
	if err != nil {
		panic(err)
	}
	return port, addr
}
