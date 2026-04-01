package resolver

import (
	"context"

	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/plane/data/balancer"
	"github.com/fireflycore/service-mesh/source"
)

// Resolver 把“逻辑目标服务”解析为“本次实际要访问的 endpoint”。
type Resolver struct {
	// source 提供某个逻辑服务当前有哪些实例。
	source source.Provider
	// balancer 决定这些实例里本次该选哪一个。
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
	// 先拿到全量快照，再把选点策略留给 balancer。
	snapshot, err := r.source.Resolve(ctx, target)
	if err != nil {
		return model.Endpoint{}, err
	}
	if snapshot.Status == model.SnapshotStatusDegraded {
		return model.Endpoint{}, &SnapshotStatusError{
			Target: target,
			Status: snapshot.Status,
			Reason: snapshot.StatusReason,
		}
	}

	// balancer 在这里把“服务级快照”压缩成“单个目标实例”。
	return r.balancer.Pick(snapshot)
}
