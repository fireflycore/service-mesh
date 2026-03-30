package source

import (
	"context"
	"errors"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// SnapshotResolver 抽象一个“能提供高优先级快照覆盖”的状态源。
type SnapshotResolver interface {
	ResolveSnapshot(target model.ServiceRef) (model.ServiceSnapshot, bool)
}

type TargetTracker interface {
	TrackTarget(target model.ServiceRef)
}

// ErrControlPlaneSnapshotUnavailable 表示当前目标服务还没有可用控制面快照。
var ErrControlPlaneSnapshotUnavailable = errors.New("controlplane snapshot unavailable")

// Overlay 用于实现“controlplane 优先，本地目录回退”。
type Overlay struct {
	// primary 是原始目录来源，priority 是更高优先级的覆盖来源。
	primary              Provider
	priority             SnapshotResolver
	allowPrimaryFallback bool
}

// NewOverlay 创建一个覆盖式目录源。
func NewOverlay(primary Provider, priority SnapshotResolver) *Overlay {
	return NewOverlayWithFallback(primary, priority, true)
}

// NewOverlayWithFallback 创建一个可配置是否回退原始目录源的覆盖式目录源。
func NewOverlayWithFallback(primary Provider, priority SnapshotResolver, allowPrimaryFallback bool) *Overlay {
	return &Overlay{
		primary:              primary,
		priority:             priority,
		allowPrimaryFallback: allowPrimaryFallback,
	}
}

// Name 返回组合后 provider 的名字，便于调试时识别来源。
func (o *Overlay) Name() string {
	if !o.allowPrimaryFallback {
		return "controlplane-primary"
	}
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
		if tracker, ok := o.priority.(TargetTracker); ok {
			tracker.TrackTarget(target)
		}
	}

	if !o.allowPrimaryFallback || o.primary == nil {
		// 第十四版开始，controlplane 开启时默认走主路径；未命中时显式报“快照未就绪”。
		return model.ServiceSnapshot{}, ErrControlPlaneSnapshotUnavailable
	}

	// 控制面没有覆盖时，再回退到 consul/etcd 等原始目录来源。
	return o.primary.Resolve(ctx, target)
}
