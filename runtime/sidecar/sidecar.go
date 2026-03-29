package sidecar

import (
	"log/slog"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/runtime/shared"
)

type Runner = shared.Runner

func New(cfg *config.Config) (*Runner, error) {
	return shared.New(cfg, shared.Params{
		Mode:        "sidecar",
		Listen:      cfg.Runtime.Sidecar.Listen,
		ServiceName: cfg.Runtime.Sidecar.ServiceName,
		InstanceID:  cfg.Runtime.Sidecar.InstanceID,
		LogAttributes: []slog.Attr{
			slog.String("service_name", cfg.Runtime.Sidecar.ServiceName),
			slog.String("instance_id", cfg.Runtime.Sidecar.InstanceID),
			slog.String("source_kind", cfg.Source.Kind),
			slog.String("authz_target", cfg.Authz.Target),
		},
	})
}
