package snapshot

import (
	"context"
	"testing"

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
