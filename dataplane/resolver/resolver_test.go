package resolver

import (
	"context"
	"errors"
	"testing"

	"github.com/fireflycore/service-mesh/dataplane/balancer"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/memory"
)

func TestResolverRejectsDegradedSnapshot(t *testing.T) {
	r := New(memory.New(map[string]model.ServiceSnapshot{
		"default/dev/orders": {
			Service: model.ServiceRef{
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
			Endpoints: []model.Endpoint{
				{Address: "10.0.0.70", Port: 19090, Weight: 1},
			},
			Status:       model.SnapshotStatusDegraded,
			StatusReason: "all endpoints unhealthy",
		},
	}), balancer.NewRoundRobin())

	_, err := r.Resolve(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err == nil {
		t.Fatal("expected degraded snapshot to fail")
	}
	if !errors.Is(err, ErrSnapshotDegraded) {
		t.Fatalf("expected degraded snapshot error, got: %v", err)
	}
}

func TestResolverAllowsStaleSnapshot(t *testing.T) {
	r := New(memory.New(map[string]model.ServiceSnapshot{
		"default/dev/orders": {
			Service: model.ServiceRef{
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
			Endpoints: []model.Endpoint{
				{Address: "10.0.0.71", Port: 19090, Weight: 1},
			},
			Status:       model.SnapshotStatusStale,
			StatusReason: "registry unavailable",
		},
	}), balancer.NewRoundRobin())

	endpoint, err := r.Resolve(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("expected stale snapshot to still resolve: %v", err)
	}
	if got, want := endpoint.Address, "10.0.0.71"; got != want {
		t.Fatalf("unexpected resolved endpoint: got=%s want=%s", got, want)
	}
}
