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
)

const (
	admin = "ceph-e2e-test-admin"
	pass  = "ceph-e2e-test-pass"
)

func TestMain(m *testing.M) {
	err := config.Get(&conf)
	if err != nil {
		panic(err)
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
	go func() {
		appCtx, cancelFn := context.WithCancel(ctx)
		defer cancelFn()
		err = app.Start(appCtx, conf, config.Build{Version: "test"})
		if err != nil {
			panic(err)
		}
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
		panic(err)
	}

	c, err := cephapi.New(tstCtx, cephapi.ClientConfig{
		GrpcUrl:  grpcAddr,
		HttpUrl:  httpAddr,
		Login:    admin,
		Password: pass,
	})
	if err != nil {
		panic(err)
	}
	admConn = c.Conn()
	exitCode := m.Run()
	cancel()
	grpcConn.Close()
	c.Close()
	// exit
	os.Exit(exitCode)
}

func getRandomPort() (int, string) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(err)
	}
	addr := l.Addr().String()
	addrs := strings.Split(addr, ":")
	err = l.Close()
	if err != nil {
		panic(err)
	}

	port, err := strconv.Atoi(addrs[len(addrs)-1])
	if err != nil {
		panic(err)
	}
	return port, addr
}
