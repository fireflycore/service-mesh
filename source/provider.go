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

// FromConfig 根据配置选择具体的目录来源实现。
//
// 当前第三版优先支持：
// - 真实 consul source
// - etcd 先保留接口与占位实现
func FromConfig(cfg config.SourceConfig) (Provider, error) {
	switch cfg.Kind {
	case model.SourceConsul:
		return consul.New(cfg.Consul)
	case model.SourceEtcd:
		return etcd.New(cfg.Etcd), nil
	default:
		return nil, fmt.Errorf("unsupported source kind: %s", cfg.Kind)
	}
}
