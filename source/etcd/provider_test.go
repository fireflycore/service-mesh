package etcd

import (
	"context"
	"errors"
	"testing"
	"time"

	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type fakeKVGetter struct {
	response *clientv3.GetResponse
	err      error
	delay    time.Duration
}

// Get 用固定返回值模拟 etcd KV 查询结果。
func (f fakeKVGetter) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return f.response, f.err
}

// TestProviderResolve 验证 etcd provider 能正确解析注册 JSON。
func TestProviderResolve(t *testing.T) {
	// 这里用一条最小合法 JSON 注册值，验证 etcd provider 的解析主路径。
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
	// 如果 Resolve 成功，说明 provider 已经走通了：
	// 目录前缀拼装 -> KV 查询 -> JSON 解析 -> Endpoint 转换。

	if got, want := len(snapshot.Endpoints), 1; got != want {
		t.Fatalf("unexpected endpoint count: got=%d want=%d", got, want)
	}
	// 下面三项断言分别覆盖 host、port、weight 三个核心提取字段。
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

func TestProviderResolveHonorsQueryTimeout(t *testing.T) {
	provider := &Provider{
		Config: config.EtcdSourceConfig{
			Namespace:      "/microservice/lhdht",
			QueryTimeoutMS: 20,
		},
		client: fakeKVGetter{
			delay: 200 * time.Millisecond,
		},
	}

	_, err := provider.Resolve(context.Background(), model.ServiceRef{
		Service:   "orders",
		Namespace: "/microservice/lhdht",
		Env:       "dev",
	})
	if err == nil {
		t.Fatal("expected resolve timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got: %v", err)
	}
}
