package watchapi

import (
	"context"
	"reflect"
	"time"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type SnapshotPoller func(context.Context) (model.ServiceSnapshot, bool, error)

func RunPolling(ctx context.Context, interval time.Duration, target model.ServiceRef, poll SnapshotPoller) Stream {
	if interval <= 0 {
		interval = time.Second
	}

	stream := newPollingStream()
	go func() {
		defer stream.Close()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var last model.ServiceSnapshot
		var hasLast bool
		run := func() {
			snapshot, found, err := poll(ctx)
			if err != nil {
				return
			}
			if !found {
				if hasLast {
					hasLast = false
					last = model.ServiceSnapshot{}
					stream.publish(Event{
						Kind:   EventDelete,
						Target: target,
					})
				}
				return
			}
			if !hasLast || !reflect.DeepEqual(last, snapshot) {
				hasLast = true
				last = snapshot
				stream.publish(Event{
					Kind:     EventUpsert,
					Target:   snapshot.Service,
					Snapshot: snapshot,
				})
			}
		}

		run()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()

	return stream
}

type pollingStream struct {
	events chan Event
}

func newPollingStream() *pollingStream {
	return &pollingStream{
		events: make(chan Event, 8),
	}
}

func (s *pollingStream) Events() <-chan Event {
	return s.events
}

func (s *pollingStream) Close() error {
	defer func() {
		recover()
	}()
	close(s.events)
	return nil
}

func (s *pollingStream) publish(event Event) {
	select {
	case s.events <- event:
	default:
	}
}
