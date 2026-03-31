package watchapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fireflycore/service-mesh/pkg/model"
)

func TestRunPollingEmitsUpsertAndDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var state int
	stream := RunPolling(ctx, 10*time.Millisecond, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	}, func(context.Context) (model.ServiceSnapshot, bool, error) {
		switch state {
		case 0:
			return model.ServiceSnapshot{
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{
					{Address: "10.0.0.10", Port: 19090, Weight: 1},
				},
			}, true, nil
		default:
			return model.ServiceSnapshot{}, false, nil
		}
	})
	defer stream.Close()

	select {
	case event := <-stream.Events():
		if got, want := event.Kind, EventUpsert; got != want {
			t.Fatalf("unexpected first event kind: got=%s want=%s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected first polling upsert event")
	}

	state = 1

	select {
	case event := <-stream.Events():
		if got, want := event.Kind, EventDelete; got != want {
			t.Fatalf("unexpected second event kind: got=%s want=%s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected second polling delete event")
	}
}

func TestRunPollingEmitsStaleSnapshotOnPollError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var state int
	stream := RunPolling(ctx, 10*time.Millisecond, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	}, func(context.Context) (model.ServiceSnapshot, bool, error) {
		switch state {
		case 0:
			return model.ServiceSnapshot{
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{
					{Address: "10.0.0.10", Port: 19090, Weight: 1},
				},
			}, true, nil
		default:
			return model.ServiceSnapshot{}, false, errors.New("registry unavailable")
		}
	})
	defer stream.Close()

	select {
	case <-stream.Events():
	case <-time.After(time.Second):
		t.Fatal("expected initial upsert event")
	}

	state = 1

	select {
	case event := <-stream.Events():
		if got, want := event.Kind, EventUpsert; got != want {
			t.Fatalf("unexpected stale event kind: got=%s want=%s", got, want)
		}
		if got, want := event.Snapshot.Status, model.SnapshotStatusStale; got != want {
			t.Fatalf("unexpected stale snapshot status: got=%s want=%s", got, want)
		}
		if event.Snapshot.StatusReason == "" {
			t.Fatal("expected stale snapshot reason")
		}
	case <-time.After(time.Second):
		t.Fatal("expected stale polling upsert event")
	}
}
