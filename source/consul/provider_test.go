package consul

import (
	"context"
	"testing"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/hashicorp/consul/api"
)

type fakeHealth struct {
	rows []*api.ServiceEntry
	err  error
}

// Service 用固定返回值模拟 Consul Health API。
func (f fakeHealth) Service(service, tag string, passingOnly bool, q *api.QueryOptions) ([]*api.ServiceEntry, *api.QueryMeta, error) {
	return f.rows, &api.QueryMeta{}, f.err
}

// TestProviderResolve 验证 Consul provider 能把健康实例转成内部快照。
func TestProviderResolve(t *testing.T) {
	provider := &Provider{
		Config: config.ConsulSourceConfig{
			Address: "127.0.0.1:8500",
		},
		health: fakeHealth{
			rows: []*api.ServiceEntry{
				{
					Service: &api.AgentService{
						Service: "orders",
						Address: "10.0.0.12",
						Port:    19090,
					},
				},
			},
		},
	}

	snapshot, err := provider.Resolve(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if got, want := len(snapshot.Endpoints), 1; got != want {
		t.Fatalf("unexpected endpoint count: got=%d want=%d", got, want)
	}
	if got, want := snapshot.Endpoints[0].Address, "10.0.0.12"; got != want {
		t.Fatalf("unexpected endpoint address: got=%s want=%s", got, want)
	}
	if got, want := snapshot.Endpoints[0].Port, uint32(19090); got != want {
		t.Fatalf("unexpected endpoint port: got=%d want=%d", got, want)
	}
}
