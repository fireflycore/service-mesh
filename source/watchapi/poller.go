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

		var current model.ServiceSnapshot
		var hasCurrent bool
		var emitted model.ServiceSnapshot
		var hasEmitted bool
		run := func() {
			snapshot, found, err := poll(ctx)
			if err != nil {
				if hasCurrent {
					stale := current
					stale.Status = model.SnapshotStatusStale
					stale.StatusReason = err.Error()
					if !hasEmitted || !reflect.DeepEqual(emitted, stale) {
						emitted = stale
						hasEmitted = true
						stream.publish(Event{
							Kind:     EventUpsert,
							Target:   stale.Service,
							Snapshot: stale,
						})
					}
				}
				return
			}
			if !found {
				if hasEmitted {
					hasCurrent = false
					current = model.ServiceSnapshot{}
					emitted = model.ServiceSnapshot{}
					hasEmitted = false
					stream.publish(Event{
						Kind:   EventDelete,
						Target: target,
					})
				}
				return
			}
			current = snapshot
			hasCurrent = true
			if snapshot.Status == "" {
				snapshot.Status = model.SnapshotStatusCurrent
			}
			snapshot.StatusReason = ""
			if !hasEmitted || !reflect.DeepEqual(emitted, snapshot) {
				emitted = snapshot
				hasEmitted = true
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
