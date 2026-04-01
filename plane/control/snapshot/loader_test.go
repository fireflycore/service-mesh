package snapshot

import (
	"context"
	"testing"
	"time"

	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/memory"
)

func TestLoaderRefreshCachesSourceSnapshot(t *testing.T) {
	store := NewStore()
	loader := NewLoader(
		store,
		memory.New(map[string]model.ServiceSnapshot{
			"default/dev/orders": {
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{
					{Address: "10.0.0.10", Port: 19090, Weight: 1},
				},
			},
		}),
	)

	snapshot, changed, err := loader.Refresh(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if !changed {
		t.Fatal("expected first refresh to update cache")
	}
	if snapshot == nil || snapshot.GetRevision() == "" {
		t.Fatal("expected generated revision for source snapshot")
	}

	lookup, policy := store.Lookup(snapshot.GetService())
	if policy != nil {
		t.Fatal("expected no route policy in store")
	}
	if lookup == nil {
		t.Fatal("expected snapshot to be cached in store")
	}
	if got, want := lookup.GetEndpoints()[0].GetAddress(), "10.0.0.10"; got != want {
		t.Fatalf("unexpected cached endpoint: got=%s want=%s", got, want)
	}
}

func TestLoaderRefreshDoesNotRewriteUnchangedSnapshot(t *testing.T) {
	store := NewStore()
	loader := NewLoader(
		store,
		memory.New(map[string]model.ServiceSnapshot{
			"default/dev/orders": {
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{
					{Address: "10.0.0.10", Port: 19090, Weight: 1},
				},
			},
		}),
	)

	first, changed, err := loader.Refresh(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("first refresh failed: %v", err)
	}
	if !changed {
		t.Fatal("expected first refresh to update cache")
	}

	second, changed, err := loader.Refresh(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("second refresh failed: %v", err)
	}
	if changed {
		t.Fatal("expected unchanged source snapshot to keep cached revision")
	}
	if got, want := second.GetRevision(), first.GetRevision(); got != want {
		t.Fatalf("unexpected revision after unchanged refresh: got=%s want=%s", got, want)
	}
}

func TestLoaderWatchBridgesUpsertAndDelete(t *testing.T) {
	provider := memory.New(nil)
	store := NewStore()
	loader := NewLoader(store, provider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates, err := loader.Watch(ctx, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	if updates == nil {
		t.Fatal("expected watch-capable provider to return updates channel")
	}

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
	case update := <-updates:
		if !update.Changed || update.Snapshot == nil {
			t.Fatal("expected changed upsert update")
		}
		if got, want := update.Snapshot.GetEndpoints()[0].GetAddress(), "10.0.0.10"; got != want {
			t.Fatalf("unexpected upsert snapshot address: got=%s want=%s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected upsert watch update")
	}

	provider.Delete(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	select {
	case update := <-updates:
		if !update.Changed || !update.Deleted {
			t.Fatal("expected changed delete update")
		}
	case <-time.After(time.Second):
		t.Fatal("expected delete watch update")
	}
}
