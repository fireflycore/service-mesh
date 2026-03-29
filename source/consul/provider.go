package consul

import "github.com/fireflycore/service-mesh/pkg/config"

type Provider struct {
	Config config.ConsulSourceConfig
}

func New(cfg config.ConsulSourceConfig) *Provider {
	return &Provider{Config: cfg}
}

func (p *Provider) Name() string {
	return "consul"
}
