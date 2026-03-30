package agent

import (
	"log/slog"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/runtime/shared"
)

// Runner 只是 shared.Runner 的 agent 视图。
type Runner = shared.Runner

// New 用 agent 专属参数包装 shared runner。
func New(cfg *config.Config) (*Runner, error) {
	return shared.New(cfg, shared.Params{
		// agent 以共享本地网关身份运行。
		Mode: "agent",
		// agent 监听地址来自 runtime.agent.address。
		Address: cfg.Runtime.Agent.Address,
		// controlplane 里固定以 service-mesh-agent 作为逻辑服务名注册。
		ServiceName: "service-mesh-agent",
		LogAttributes: []slog.Attr{
			slog.String("source_kind", cfg.Source.Kind),
			slog.String("authz_target", cfg.Authz.Target),
		},
	})
}
