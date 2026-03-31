package memory

import (
	"context"
	"testing"
	"time"

	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source"
)

func TestProviderWatchReceivesUpsertAndDelete(t *testing.T) {
	provider := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := provider.Watch(ctx, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	defer stream.Close()

	provider.Upsert(model.ServiceSnapshot{
		Service: model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []model.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
	})

	select {
	case event := <-stream.Events():
		if got, want := event.Kind, source.WatchEventUpsert; got != want {
			t.Fatalf("unexpected watch event kind: got=%s want=%s", got, want)
		}
		if got, want := event.Snapshot.Endpoints[0].Address, "10.0.0.10"; got != want {
			t.Fatalf("unexpected snapshot address: got=%s want=%s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected upsert event")
	}

	provider.Delete(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	select {
	case event := <-stream.Events():
		if got, want := event.Kind, source.WatchEventDelete; got != want {
			t.Fatalf("unexpected delete event kind: got=%s want=%s", got, want)
		}
		if got, want := event.Target.Service, "orders"; got != want {
			t.Fatalf("unexpected delete target: got=%s want=%s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected delete event")
	}
}
