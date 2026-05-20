//go:build cgo

package test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	cephapi "github.com/clyso/ceph-api"
	"github.com/clyso/ceph-api/pkg/app"
	"github.com/clyso/ceph-api/pkg/config"
	"github.com/clyso/ceph-api/test/testenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	conf     config.Config
	grpcAddr string
	httpAddr string
	tstCtx   context.Context
	grpcConn *grpc.ClientConn
	admConn  *grpc.ClientConn

	cephEnv *testenv.CephEnv
)

const (
	admin = "ceph-e2e-test-admin"
	pass  = "ceph-e2e-test-pass"
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

	tlsOpt := grpc.WithInsecure()
	if conf.Api.Secure {
		tlsOpt = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}))
	}
	grpcConn, err = grpc.DialContext(ctx, grpcAddr,
		tlsOpt,
		grpc.WithBackoffMaxDelay(time.Second),
		grpc.WithBlock(),
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

	exitCode := m.Run()
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
