package source

import (
	"context"
	"testing"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type fakeProvider struct {
	// snapshot 是底层目录源会返回的预置结果。
	snapshot model.ServiceSnapshot
}

// Name 返回测试用 provider 名称。
func (p fakeProvider) Name() string {
	return "fake"
}

// Resolve 直接返回预置快照。
func (p fakeProvider) Resolve(context.Context, model.ServiceRef) (model.ServiceSnapshot, error) {
	return p.snapshot, nil
}

type fakeSnapshotResolver struct {
	// snapshot 是控制面优先覆盖时应返回的结果。
	snapshot model.ServiceSnapshot
	ok       bool
}

// ResolveSnapshot 直接返回预置控制面快照。
func (r fakeSnapshotResolver) ResolveSnapshot(target model.ServiceRef) (model.ServiceSnapshot, bool) {
	return r.snapshot, r.ok
}

// TestOverlayUsesControlPlaneSnapshotFirst 验证 overlay 的优先级是 controlplane first。
func TestOverlayUsesControlPlaneSnapshotFirst(t *testing.T) {
	// 同时准备“底层目录快照”和“控制面覆盖快照”，验证谁优先生效。
	overlay := NewOverlay(
		fakeProvider{
			snapshot: model.ServiceSnapshot{
				Service:   model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
				Endpoints: []model.Endpoint{{Address: "10.0.0.1", Port: 19090, Weight: 1}},
			},
		},
		fakeSnapshotResolver{
			snapshot: model.ServiceSnapshot{
				Service:   model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
				Endpoints: []model.Endpoint{{Address: "10.0.0.2", Port: 29090, Weight: 1}},
			},
			ok: true,
		},
	)

	snapshot, err := overlay.Resolve(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	// 期望拿到的是控制面快照地址 10.0.0.2，而不是底层目录的 10.0.0.1。
	if got, want := snapshot.Endpoints[0].Address, "10.0.0.2"; got != want {
		t.Fatalf("unexpected overlay endpoint: got=%s want=%s", got, want)
	}
}

func TestOverlayReturnsSnapshotUnavailableWithoutFallback(t *testing.T) {
	overlay := NewOverlayWithFallback(nil, fakeSnapshotResolver{}, false)

	_, err := overlay.Resolve(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err == nil {
		t.Fatal("expected missing controlplane snapshot to fail")
	}
	if err != ErrControlPlaneSnapshotUnavailable {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOverlayFallsBackToPrimaryWhenEnabled(t *testing.T) {
	overlay := NewOverlayWithFallback(
		fakeProvider{
			snapshot: model.ServiceSnapshot{
				Service:   model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
				Endpoints: []model.Endpoint{{Address: "10.0.0.3", Port: 39090, Weight: 1}},
			},
		},
		fakeSnapshotResolver{},
		true,
	)

	snapshot, err := overlay.Resolve(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got, want := snapshot.Endpoints[0].Address, "10.0.0.3"; got != want {
		t.Fatalf("unexpected fallback endpoint: got=%s want=%s", got, want)
	}
}
