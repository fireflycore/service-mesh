package sidecar

import (
	"log/slog"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/runtime/shared"
)

// Runner 只是 shared.Runner 的 sidecar 视图。
type Runner = shared.Runner

// New 用 sidecar 专属身份与监听参数包装 shared runner。
func New(cfg *config.Config) (*Runner, error) {
	return shared.New(cfg, shared.Params{
		Mode:        "sidecar",
		Listen:      cfg.Runtime.Sidecar.Listen,
		ServiceName: cfg.Runtime.Sidecar.ServiceName,
		InstanceID:  cfg.Runtime.Sidecar.InstanceID,
		Namespace:   cfg.Runtime.Sidecar.Namespace,
		Env:         cfg.Runtime.Sidecar.Env,
		LogAttributes: []slog.Attr{
			slog.String("service_name", cfg.Runtime.Sidecar.ServiceName),
			slog.String("instance_id", cfg.Runtime.Sidecar.InstanceID),
			slog.String("namespace", cfg.Runtime.Sidecar.Namespace),
			slog.String("env", cfg.Runtime.Sidecar.Env),
			slog.String("source_kind", cfg.Source.Kind),
			slog.String("authz_target", cfg.Authz.Target),
		},
	})
}
