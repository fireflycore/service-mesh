package watchapi

import (
	"context"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type EventKind string

const (
	EventUpsert EventKind = "upsert"
	EventDelete EventKind = "delete"
)

type Event struct {
	Kind     EventKind
	Target   model.ServiceRef
	Snapshot model.ServiceSnapshot
}

type Stream interface {
	Events() <-chan Event
	Close() error
}

type Capable interface {
	Watch(ctx context.Context, target model.ServiceRef) (Stream, error)
}
