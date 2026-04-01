package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/plane/control/server"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "mvp-controlplane",
		Short: "Run the MVP bootstrap control plane",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadControlPlaneConfig(configPath)
			if err != nil {
				return err
			}

			store := bootstrapStore(cfg)
			srv := server.New(store)
			for _, target := range cfg.TrackedTargets {
				srv.TrackTarget(toModelService(target))
			}

			grpcServer := grpc.NewServer()
			controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, srv)

			listener, err := net.Listen("tcp", cfg.Listen)
			if err != nil {
				return err
			}
			defer listener.Close()

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			errCh := make(chan error, 1)
			go func() {
				errCh <- grpcServer.Serve(listener)
			}()

			select {
			case <-ctx.Done():
				grpcServer.GracefulStop()
				return nil
			case err := <-errCh:
				return err
			}
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "control plane config file path")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}
