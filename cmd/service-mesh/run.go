package main

import (
	"context"
	"os/signal"
	"syscall"

	servicemeshapp "github.com/fireflycore/service-mesh/app/service-mesh"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var opts config.LoadOptions

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run service-mesh runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(opts)
			if err != nil {
				return err
			}

			app, err := servicemeshapp.New(cfg)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return app.Run(ctx)
		},
	}

	bindCommonFlags(cmd, &opts)
	return cmd
}
