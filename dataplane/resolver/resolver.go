package resolver

import (
	"context"

	"github.com/fireflycore/service-mesh/dataplane/balancer"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source"
)

type Resolver struct {
	source   source.Provider
	balancer balancer.Picker
}

// New 创建最小 resolver。
//
// resolver 的职责刻意保持单一：
// - 先向 source 拉 ServiceSnapshot
// - 再交给 balancer 选出一个 endpoint
func New(provider source.Provider, picker balancer.Picker) *Resolver {
	return &Resolver{
		source:   provider,
		balancer: picker,
	}
}

// Resolve 返回本次调用应命中的目标 endpoint。
func (r *Resolver) Resolve(ctx context.Context, target model.ServiceRef) (model.Endpoint, error) {
	snapshot, err := r.source.Resolve(ctx, target)
	if err != nil {
		return model.Endpoint{}, err
	}

	return r.balancer.Pick(snapshot)
}
