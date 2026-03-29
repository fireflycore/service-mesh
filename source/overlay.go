package source

import (
	"context"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// SnapshotResolver 抽象一个“能提供高优先级快照覆盖”的状态源。
type SnapshotResolver interface {
	ResolveSnapshot(target model.ServiceRef) (model.ServiceSnapshot, bool)
}

// Overlay 用于实现“controlplane 优先，本地目录回退”。
type Overlay struct {
	// primary 是原始目录来源，priority 是更高优先级的覆盖来源。
	primary  Provider
	priority SnapshotResolver
}

// NewOverlay 创建一个覆盖式目录源。
func NewOverlay(primary Provider, priority SnapshotResolver) *Overlay {
	return &Overlay{
		primary:  primary,
		priority: priority,
	}
}

// Name 返回组合后 provider 的名字，便于调试时识别来源。
func (o *Overlay) Name() string {
	if o.primary == nil {
		return "overlay"
	}
	return "overlay(" + o.primary.Name() + ")"
}

// Resolve 优先读取控制面快照，失败后再回退到底层目录源。
func (o *Overlay) Resolve(ctx context.Context, target model.ServiceRef) (model.ServiceSnapshot, error) {
	if o.priority != nil {
		// 只要控制面快照命中，就不再访问底层目录服务。
		if snapshot, ok := o.priority.ResolveSnapshot(target); ok {
			return snapshot, nil
		}
	}

	// 控制面没有覆盖时，再回退到 consul/etcd 等原始目录来源。
	return o.primary.Resolve(ctx, target)
}
