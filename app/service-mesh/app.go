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

type App struct {
	Config *config.Config
	Runner runtime.Runner
}

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

func (a *App) Run(ctx context.Context) error {
	return a.Runner.Run(ctx)
}
