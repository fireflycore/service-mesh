package source

import (
	"context"
	"fmt"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/consul"
	"github.com/fireflycore/service-mesh/source/etcd"
)

type Provider interface {
	Name() string
	Resolve(ctx context.Context, target model.ServiceRef) (model.ServiceSnapshot, error)
}

func FromConfig(cfg config.SourceConfig) (Provider, error) {
	switch cfg.Kind {
	case model.SourceConsul:
		return consul.New(cfg.Consul), nil
	case model.SourceEtcd:
		return etcd.New(cfg.Etcd), nil
	default:
		return nil, fmt.Errorf("unsupported source kind: %s", cfg.Kind)
	}
}
