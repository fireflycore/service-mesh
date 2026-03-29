package consul

import (
	"context"
	"errors"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type Provider struct {
	Config config.ConsulSourceConfig
}

func New(cfg config.ConsulSourceConfig) *Provider {
	return &Provider{Config: cfg}
}

func (p *Provider) Name() string {
	return "consul"
}

func (p *Provider) Resolve(_ context.Context, _ model.ServiceRef) (model.ServiceSnapshot, error) {
	return model.ServiceSnapshot{}, errors.New("consul source resolve is not implemented")
}
