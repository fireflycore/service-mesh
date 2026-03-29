package servicemeshapp

import (
	"context"
	"fmt"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/runtime"
	"github.com/fireflycore/service-mesh/runtime/agent"
	"github.com/fireflycore/service-mesh/runtime/sidecar"
)

// App 是 CLI 层拿到的顶层运行对象。
//
// 它只保留两件事情：
// - 已经完成规范化和校验的配置
// - 根据 mode 选择好的具体 runtime.Runner
type App struct {
	// Config 指向已经完成默认值补齐和校验的最终配置。
	Config *config.Config
	// Runner 是按 mode 选出的具体运行时实现。
	Runner runtime.Runner
}

// New 根据 mode 装配顶层应用。
//
// 这里故意不把 mode 分支展开到 CLI 层，
// 而是集中在 App 内统一选择 agent / sidecar。
func New(cfg *config.Config) (*App, error) {
	var runner runtime.Runner
	var err error

	switch cfg.Mode {
	case model.ModeAgent:
		// agent 模式面向“同机多个业务共享一个本地网关入口”的场景。
		runner, err = agent.New(cfg)
		if err != nil {
			return nil, err
		}
	case model.ModeSidecar:
		// sidecar 模式面向“单业务实例绑定一个本地代理”的场景。
		runner, err = sidecar.New(cfg)
		if err != nil {
			return nil, err
		}
	default:
		// 这里保留显式错误，避免 mode 拼写错误时静默落到错误运行模式。
		return nil, fmt.Errorf("unsupported mode: %s", cfg.Mode)
	}

	return &App{
		Config: cfg,
		Runner: runner,
	}, nil
}

// Run 直接把控制权交给底层 runner。
func (a *App) Run(ctx context.Context) error {
	return a.Runner.Run(ctx)
}
