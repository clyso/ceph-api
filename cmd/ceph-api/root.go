package main

import "github.com/spf13/cobra"

type rootOptions struct {
	configPath         string
	configOverridePath string
}

func newRootCmd() *cobra.Command {
	opts := &rootOptions{}

	cmd := &cobra.Command{
		Use:           "ceph-api",
		Short:         "Ceph API server and client",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), opts)
		},
	}

	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "", "set path to config directory")
	cmd.PersistentFlags().StringVar(&opts.configOverridePath, "config-override", "", "set path to config override directory")

	cmd.AddCommand(newServeCmd(opts))
	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newVersionCmd())

	return cmd
}
