package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/soheilhy/cmux"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

func Serve(ctx context.Context, conf Config, grpcServer *grpc.Server, httpServer http.Handler) (start func(context.Context) error, stop func(context.Context) error, err error) {
	if conf.GrpcPort == conf.HttpPort {
		// Serve grpc and http on the same port
		zerolog.Ctx(ctx).Info().Msgf("serving grpc and http API on the same port %d", conf.HttpPort)
		var lis net.Listener
		if conf.Secure {
			var tlsConf *tls.Config
			tlsConf, err = selfIssuedTlsConf()
			if err != nil {
				return nil, nil, err
			}
			lis, err = tls.Listen("tcp", fmt.Sprintf(":%d", conf.HttpPort), tlsConf)
		} else {
			lis, err = net.Listen("tcp", fmt.Sprintf(":%d", conf.HttpPort))
		}
		if err != nil {
			return nil, nil, err
		}
		mux := cmux.New(lis)
		grpcL := mux.Match(cmux.HTTP2())
		httpL := mux.Match(cmux.HTTP1Fast())
		srv := &http.Server{
			ReadHeaderTimeout: time.Second * 5,
			Handler:           httpServer,
		}

		start = func(ctx context.Context) error {
			g, gCtx := errgroup.WithContext(ctx)
			g.Go(func() error {
				err := mux.Serve()
				if err != nil {
					zerolog.Ctx(gCtx).Err(err).Msg("unable to start cmux server")
				}
				return err
			})
			g.Go(func() error {
				err := grpcServer.Serve(grpcL)
				if err != nil {
					zerolog.Ctx(gCtx).Err(err).Msg("unable to start grpc server")
				}
				return err
			})
			g.Go(func() error {
				err := srv.Serve(httpL)
				if err != nil {
					zerolog.Ctx(gCtx).Err(err).Msg("unable to start http server")
				}
				return err
			})
			return g.Wait()
		}

		stop = func(ctx context.Context) error {
			err := srv.Shutdown(context.Background())
			if err != nil {
				zerolog.Ctx(ctx).Err(err).Msg("unable to close http server")
			}
			err = httpL.Close()
			if err != nil {
				zerolog.Ctx(ctx).Err(err).Msg("unable to close http listener")
			}
			grpcServer.GracefulStop()
			err = grpcL.Close()
			if err != nil {
				zerolog.Ctx(ctx).Err(err).Msg("unable to close grpc listener")
			}
			mux.Close()

			return nil
		}
		return
	}
	// Serve http and grpc on different ports
	// TODO: support TLS
	srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", conf.HttpPort)}
	srv.Handler = httpServer

	grpcLis, err := net.Listen("tcp", fmt.Sprintf(":%d", conf.GrpcPort))
	if err != nil {
		return nil, nil, err
	}

	start = func(ctx context.Context) error {
		g, gCtx := errgroup.WithContext(ctx)
		g.Go(func() error {
			err := srv.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				zerolog.Ctx(gCtx).Err(err).Msg("unable to start http server")
			}
			return err
		})
		g.Go(func() error {
			err := grpcServer.Serve(grpcLis)
			if err != nil {
				zerolog.Ctx(gCtx).Err(err).Msg("unable to start grpc server")
			}
			return err
		})
		return g.Wait()
	}

	stop = func(ctx context.Context) error {
		err := srv.Shutdown(context.Background())
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("unable to close http server")
		}
		grpcServer.GracefulStop()
		return nil
	}
	return
}

func selfIssuedTlsConf() (*tls.Config, error) {
	cert, err := genX509KeyPair()
	if err != nil {
		return nil, err
	}
	tlsConf := &tls.Config{}
	tlsConf.Certificates = make([]tls.Certificate, 1)
	tlsConf.Certificates[0] = cert
	return tlsConf, nil
}

func genX509KeyPair() (tls.Certificate, error) {
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.Unix()),
		Subject: pkix.Name{
			CommonName:         "seaphony.github.com",
			Country:            []string{"Germany"},
			Organization:       []string{"seaphony.io"},
			OrganizationalUnit: []string{"ceph"},
		},
		NotBefore:             now,
		NotAfter:              now.AddDate(1, 0, 0), // Valid for one year
		SubjectKeyId:          []byte{113, 117, 105, 99, 107, 115, 101, 114, 118, 101},
		BasicConstraintsValid: true,
		//IsCA:                  true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage: x509.KeyUsageKeyEncipherment |
			x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	cert, err := x509.CreateCertificate(rand.Reader, template, template,
		priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	var outCert tls.Certificate
	outCert.Certificate = append(outCert.Certificate, cert)
	outCert.PrivateKey = priv

	return outCert, nil
}
