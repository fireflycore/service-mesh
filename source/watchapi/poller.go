package watchapi

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type SnapshotPoller func(context.Context) (model.ServiceSnapshot, bool, error)

const (
	defaultDegradeAfterConsecutiveErrors = 3
	ErrorClassNotFound                   = "not_found"
	ErrorClassTimeout                    = "timeout"
	ErrorClassUnavailable                = "unavailable"
	ErrorClassInternal                   = "internal"
)

type PollingOptions struct {
	DegradeAfterConsecutiveErrors int
}

func RunPolling(ctx context.Context, interval time.Duration, target model.ServiceRef, poll SnapshotPoller) Stream {
	return RunPollingWithOptions(ctx, interval, target, PollingOptions{}, poll)
}

func RunPollingWithOptions(ctx context.Context, interval time.Duration, target model.ServiceRef, options PollingOptions, poll SnapshotPoller) Stream {
	if interval <= 0 {
		interval = time.Second
	}
	degradeAfter := options.DegradeAfterConsecutiveErrors
	if degradeAfter <= 0 {
		degradeAfter = defaultDegradeAfterConsecutiveErrors
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
		consecutiveErrors := 0
		run := func() {
			snapshot, found, err := poll(ctx)
			if err != nil {
				consecutiveErrors++
				reason := formatPollingErrorReason(err)
				if consecutiveErrors >= degradeAfter {
					degraded := current
					if !hasCurrent {
						degraded = model.ServiceSnapshot{Service: target}
					}
					degraded.Status = model.SnapshotStatusDegraded
					degraded.StatusReason = reason
					if !hasEmitted || !reflect.DeepEqual(emitted, degraded) {
						emitted = degraded
						hasEmitted = true
						stream.publish(Event{
							Kind:     EventUpsert,
							Target:   degraded.Service,
							Snapshot: degraded,
						})
					}
					return
				}
				if hasCurrent {
					stale := current
					stale.Status = model.SnapshotStatusStale
					stale.StatusReason = reason
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
			consecutiveErrors = 0
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

func formatPollingErrorReason(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("class=%s error=%s", classifyPollingError(err), strings.TrimSpace(err.Error()))
}

func classifyPollingError(err error) string {
	switch {
	case err == nil:
		return ErrorClassInternal
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorClassTimeout
	case errors.Is(err, context.Canceled):
		return ErrorClassUnavailable
	default:
		return ErrorClassUnavailable
	}
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
