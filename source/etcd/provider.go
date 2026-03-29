package etcd

import (
	"context"
	"errors"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type Provider struct {
	Config config.EtcdSourceConfig
}

func New(cfg config.EtcdSourceConfig) *Provider {
	return &Provider{Config: cfg}
}

func (p *Provider) Name() string {
	return "etcd"
}

func (p *Provider) Resolve(_ context.Context, _ model.ServiceRef) (model.ServiceSnapshot, error) {
	return model.ServiceSnapshot{}, errors.New("etcd source resolve is not implemented")
}
