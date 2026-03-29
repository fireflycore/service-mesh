package source

import (
	"context"
	"fmt"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/consul"
	"github.com/fireflycore/service-mesh/source/etcd"
)

// Provider 抽象“如何从目录系统解析服务快照”。
type Provider interface {
	Name() string
	Resolve(ctx context.Context, target model.ServiceRef) (model.ServiceSnapshot, error)
}

// FromConfig 根据配置选择具体的目录来源实现。
//
// 当前第五版优先支持：
// - 真实 consul source
// - 真实 etcd source
func FromConfig(cfg config.SourceConfig) (Provider, error) {
	switch cfg.Kind {
	case model.SourceConsul:
		// Consul provider 适合直接复用现有 firefly 服务注册。
		return consul.New(cfg.Consul)
	case model.SourceEtcd:
		// etcd provider 适合对接 go-micro 风格的注册前缀。
		return etcd.New(cfg.Etcd)
	default:
		// 显式报错可以让调用方快速发现 source.kind 拼写问题。
		return nil, fmt.Errorf("unsupported source kind: %s", cfg.Kind)
	}
}
