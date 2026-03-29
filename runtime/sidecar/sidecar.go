package sidecar

import (
	"context"
	"log/slog"

	"github.com/fireflycore/service-mesh/pkg/config"
)

type Runner struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context) error {
	slog.Info("service-mesh sidecar started",
		slog.String("listen_network", r.cfg.Runtime.Sidecar.Listen.Network),
		slog.String("listen_address", r.cfg.Runtime.Sidecar.Listen.Address),
		slog.String("service_name", r.cfg.Runtime.Sidecar.ServiceName),
		slog.String("instance_id", r.cfg.Runtime.Sidecar.InstanceID),
	)

	<-ctx.Done()
	slog.Info("service-mesh sidecar stopped")
	return nil
}
