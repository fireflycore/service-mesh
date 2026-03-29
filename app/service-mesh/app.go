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
	Config *config.Config
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
		runner, err = agent.New(cfg)
		if err != nil {
			return nil, err
		}
	case model.ModeSidecar:
		runner, err = sidecar.New(cfg)
		if err != nil {
			return nil, err
		}
	default:
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
