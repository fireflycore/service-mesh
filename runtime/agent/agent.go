package agent

import (
	"log/slog"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/runtime/shared"
)

type Runner = shared.Runner

func New(cfg *config.Config) (*Runner, error) {
	return shared.New(cfg, shared.Params{
		Mode:        "agent",
		Listen:      cfg.Runtime.Agent.Listen,
		ServiceName: "service-mesh-agent",
		LogAttributes: []slog.Attr{
			slog.String("source_kind", cfg.Source.Kind),
			slog.String("authz_target", cfg.Authz.Target),
		},
	})
}
