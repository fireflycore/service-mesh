package agent

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
	slog.Info("service-mesh agent started",
		slog.String("listen_network", r.cfg.Runtime.Agent.Listen.Network),
		slog.String("listen_address", r.cfg.Runtime.Agent.Listen.Address),
		slog.String("source_kind", r.cfg.Source.Kind),
		slog.String("authz_target", r.cfg.Authz.Target),
	)

	<-ctx.Done()
	slog.Info("service-mesh agent stopped")
	return nil
}
