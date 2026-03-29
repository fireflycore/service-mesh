package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/dataplane/authz"
	"github.com/fireflycore/service-mesh/dataplane/balancer"
	"github.com/fireflycore/service-mesh/dataplane/invoke"
	"github.com/fireflycore/service-mesh/dataplane/resolver"
	"github.com/fireflycore/service-mesh/dataplane/transport"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/source"
	"google.golang.org/grpc"
)

type Runner struct {
	cfg        *config.Config
	grpcServer *grpc.Server
}

func New(cfg *config.Config) (*Runner, error) {
	provider, err := source.FromConfig(cfg.Source)
	if err != nil {
		return nil, err
	}

	invokeService := invoke.NewService(
		authz.NewAllowAll(),
		resolver.New(provider, balancer.NewRoundRobin()),
		transport.NewNotImplemented(),
	)

	grpcServer := grpc.NewServer()
	invokev1.RegisterMeshInvokeServiceServer(grpcServer, invokeService)

	return &Runner{
		cfg:        cfg,
		grpcServer: grpcServer,
	}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	listener, cleanup, err := r.listen()
	if err != nil {
		return err
	}
	defer cleanup()

	errCh := make(chan error, 1)
	go func() {
		if serveErr := r.grpcServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			errCh <- serveErr
		}
		close(errCh)
	}()

	slog.Info("service-mesh agent started",
		slog.String("listen_network", r.cfg.Runtime.Agent.Listen.Network),
		slog.String("listen_address", r.cfg.Runtime.Agent.Listen.Address),
		slog.String("source_kind", r.cfg.Source.Kind),
		slog.String("authz_target", r.cfg.Authz.Target),
	)

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
	}

	r.grpcServer.GracefulStop()
	slog.Info("service-mesh agent stopped")
	return nil
}

func (r *Runner) listen() (net.Listener, func(), error) {
	network := r.cfg.Runtime.Agent.Listen.Network
	address := r.cfg.Runtime.Agent.Listen.Address

	if network == "unix" {
		if err := os.RemoveAll(address); err != nil {
			return nil, nil, err
		}
	}

	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		_ = listener.Close()
		if network == "unix" {
			_ = os.Remove(address)
		}
	}

	return listener, cleanup, nil
}
