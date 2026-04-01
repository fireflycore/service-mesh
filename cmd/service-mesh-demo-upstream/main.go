package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

type demoServer struct {
	invokev1.UnimplementedMeshInvokeServiceServer
}

func (s *demoServer) UnaryInvoke(_ context.Context, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	return &invokev1.UnaryInvokeResponse{
		Payload: append([]byte("demo:"), req.GetPayload()...),
		Codec:   req.GetCodec(),
	}, nil
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "service-mesh-demo-upstream",
		Short: "Run minimal demo upstream for service-mesh MVP",
		RunE: func(cmd *cobra.Command, args []string) error {
			if listen == "" {
				listen = "127.0.0.1:50051"
			}
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			defer listener.Close()

			server := grpc.NewServer()
			invokev1.RegisterMeshInvokeServiceServer(server, &demoServer{})

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			errCh := make(chan error, 1)
			go func() {
				errCh <- server.Serve(listener)
			}()

			select {
			case <-ctx.Done():
				server.GracefulStop()
				return nil
			case err := <-errCh:
				return err
			}
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:50051", "demo upstream listen address")
	return cmd
}
