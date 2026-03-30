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
		// sidecar 以“绑定某个本地业务实例”的模式运行。
		Mode: "sidecar",
		// sidecar 本地入口通常是容器或进程附近的 TCP 地址。
		Address: cfg.Runtime.Sidecar.Address,
		// 下面四个字段共同构成 sidecar 的本地服务身份。
		ServiceName: cfg.Runtime.Sidecar.ServiceName,
		InstanceID:  cfg.Runtime.Sidecar.InstanceID,
		Namespace:   cfg.Runtime.Sidecar.Namespace,
		Env:         cfg.Runtime.Sidecar.Env,
		TargetMode:  cfg.Runtime.Sidecar.TargetMode,
		LogAttributes: []slog.Attr{
			slog.String("service_name", cfg.Runtime.Sidecar.ServiceName),
			slog.String("instance_id", cfg.Runtime.Sidecar.InstanceID),
			slog.String("namespace", cfg.Runtime.Sidecar.Namespace),
			slog.String("env", cfg.Runtime.Sidecar.Env),
			slog.String("target_mode", cfg.Runtime.Sidecar.TargetMode),
			slog.String("source_kind", cfg.Source.Kind),
			slog.String("authz_target", cfg.Authz.Target),
		},
	})
}
