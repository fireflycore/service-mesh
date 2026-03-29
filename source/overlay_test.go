package source

import (
	"context"
	"testing"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type fakeProvider struct {
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
	snapshot model.ServiceSnapshot
	ok       bool
}

// ResolveSnapshot 直接返回预置控制面快照。
func (r fakeSnapshotResolver) ResolveSnapshot(target model.ServiceRef) (model.ServiceSnapshot, bool) {
	return r.snapshot, r.ok
}

// TestOverlayUsesControlPlaneSnapshotFirst 验证 overlay 的优先级是 controlplane first。
func TestOverlayUsesControlPlaneSnapshotFirst(t *testing.T) {
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

	if got, want := snapshot.Endpoints[0].Address, "10.0.0.2"; got != want {
		t.Fatalf("unexpected overlay endpoint: got=%s want=%s", got, want)
	}
}
