package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/clyso/ceph-api/pkg/app"
	"github.com/clyso/ceph-api/pkg/config"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

func newServeCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the ceph-api server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), opts)
		},
	}
}

func runServe(parent context.Context, opts *rootOptions) error {
	var configs []config.Src
	if opts.configPath != "" {
		configs = append(configs, config.Path(opts.configPath))
	}
	if opts.configOverridePath != "" {
		configs = append(configs, config.Path(opts.configOverridePath))
	}

	ctx, cancel := signalContext(parent)
	defer cancel()

	var conf config.Config
	if err := config.Get(&conf, configs...); err != nil {
		return err
	}

	return app.Start(ctx, conf, config.Build{
		Version: version,
		Commit:  commit,
	})
}

func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGABRT, syscall.SIGTERM)

	go func() {
		select {
		case <-signals:
			zerolog.Ctx(ctx).Info().Msg("received shutdown signal.")
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(signals)
	}()

	return ctx, cancel
}
