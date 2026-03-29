package etcd

import (
	"context"
	"testing"

	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type fakeKVGetter struct {
	response *clientv3.GetResponse
	err      error
}

func (f fakeKVGetter) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	return f.response, f.err
}

func TestProviderResolve(t *testing.T) {
	provider := &Provider{
		Config: config.EtcdSourceConfig{
			Namespace: "/microservice/lhdht",
		},
		client: fakeKVGetter{
			response: &clientv3.GetResponse{
				Kvs: []*mvccpb.KeyValue{
					{
						Key:   []byte("/microservice/lhdht/dev/orders/123"),
						Value: []byte(`{"weight":10,"network":{"internal":"10.0.0.21:19090"},"meta":{"app_id":"orders","instance_id":"i-1","env":"dev","version":"v1"}}`),
					},
				},
			},
		},
	}

	snapshot, err := provider.Resolve(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "/microservice/lhdht",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if got, want := len(snapshot.Endpoints), 1; got != want {
		t.Fatalf("unexpected endpoint count: got=%d want=%d", got, want)
	}
	if got, want := snapshot.Endpoints[0].Address, "10.0.0.21"; got != want {
		t.Fatalf("unexpected address: got=%s want=%s", got, want)
	}
	if got, want := snapshot.Endpoints[0].Port, uint32(19090); got != want {
		t.Fatalf("unexpected port: got=%d want=%d", got, want)
	}
	if got, want := snapshot.Endpoints[0].Weight, uint32(10); got != want {
		t.Fatalf("unexpected weight: got=%d want=%d", got, want)
	}
}
