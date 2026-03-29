package etcd

import "github.com/fireflycore/service-mesh/pkg/config"

type Provider struct {
	Config config.EtcdSourceConfig
}

func New(cfg config.EtcdSourceConfig) *Provider {
	return &Provider{Config: cfg}
}

func (p *Provider) Name() string {
	return "etcd"
}
