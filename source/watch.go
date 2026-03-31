package source

import (
	"context"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type WatchEventKind string

const (
	WatchEventUpsert WatchEventKind = "upsert"
	WatchEventDelete WatchEventKind = "delete"
)

type WatchEvent struct {
	Kind     WatchEventKind
	Target   model.ServiceRef
	Snapshot model.ServiceSnapshot
}

type WatchStream interface {
	Events() <-chan WatchEvent
	Close() error
}

type WatchCapable interface {
	Watch(ctx context.Context, target model.ServiceRef) (WatchStream, error)
}
