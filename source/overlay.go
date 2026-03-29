package source

import (
	"context"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type SnapshotResolver interface {
	ResolveSnapshot(target model.ServiceRef) (model.ServiceSnapshot, bool)
}

type Overlay struct {
	primary  Provider
	priority SnapshotResolver
}

func NewOverlay(primary Provider, priority SnapshotResolver) *Overlay {
	return &Overlay{
		primary:  primary,
		priority: priority,
	}
}

func (o *Overlay) Name() string {
	if o.primary == nil {
		return "overlay"
	}
	return "overlay(" + o.primary.Name() + ")"
}

func (o *Overlay) Resolve(ctx context.Context, target model.ServiceRef) (model.ServiceSnapshot, error) {
	if o.priority != nil {
		if snapshot, ok := o.priority.ResolveSnapshot(target); ok {
			return snapshot, nil
		}
	}

	return o.primary.Resolve(ctx, target)
}
