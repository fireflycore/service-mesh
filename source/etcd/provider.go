package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/watchapi"
	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// kvGetter 抽象 etcd 的 Get 能力，方便单元测试时替换真实客户端。
type kvGetter interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
}

// serviceNode/network/meta 是对 etcd 注册值 JSON 的最小结构映射。
//
// 这里没有直接依赖 go-micro/registry 的结构体，
// 而是只保留 service-mesh 解析目录真正需要的字段，
// 这样可以减少不必要的模块耦合。
type serviceNode struct {
	// Weight 是注册中心里声明的实例权重。
	Weight int `json:"weight"`
	// Network 保存实例的可访问地址。
	Network *networkInfo `json:"network"`
	// Meta 里是附加业务身份信息；当前解析 endpoint 时并不直接使用全部字段。
	Meta *metaInfo `json:"meta"`
}

type networkInfo struct {
	// Internal 约定为 host:port 形式的内网地址。
	Internal string `json:"internal"`
}

type metaInfo struct {
	// 下面字段主要保留给未来更细的身份/版本路由语义扩展。
	AppID      string `json:"app_id"`
	InstanceID string `json:"instance_id"`
	Version    string `json:"version"`
	Env        string `json:"env"`
}

// Provider 是第五版引入的真实 etcd source 实现。
//
// 它的职责和 Consul provider 保持一致：
// - 从外部目录读取服务实例
// - 转换成统一的 ServiceSnapshot
// - 把后续实例选择留给 resolver/balancer
type Provider struct {
	// Config 保留连接参数与注册前缀配置。
	Config config.EtcdSourceConfig
	// client 在生产环境通常是真实 etcd client，在测试里可替换为 fake。
	client kvGetter
}

// New 创建 Etcd provider。
//
// 第五版开始，这里会真正创建 etcd client，
// 并基于统一前缀读取服务实例键值。
func New(cfg config.EtcdSourceConfig) (*Provider, error) {
	// 这里直接创建真实 etcd client；测试场景会绕开 New，直接注入 fake client。
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		Username:    cfg.Username,
		Password:    cfg.Password,
		DialTimeout: durationFromMS(cfg.DialTimeoutMS),
	})
	if err != nil {
		return nil, err
	}

	return &Provider{
		Config: cfg,
		client: client,
	}, nil
}

func (p *Provider) Name() string {
	// Name 主要用于日志与调试展示。
	return "etcd"
}

// Resolve 从 etcd 目录中读取目标服务对应的所有实例。
//
// etcd 当前沿用的注册 key 形式来自现有 firefly 体系：
//
//	<namespace>/<env>/<service>/<lease>
//
// 其中 value 是一个 JSON ServiceNode。
func (p *Provider) Resolve(ctx context.Context, target model.ServiceRef) (model.ServiceSnapshot, error) {
	queryCtx, cancel := p.queryContext(ctx)
	defer cancel()

	namespace := strings.TrimSpace(target.Namespace)
	if namespace == "" {
		// 如果调用方没写 namespace，则回退到 provider 自己的默认前缀。
		namespace = strings.TrimSpace(p.Config.Namespace)
	}
	env := strings.TrimSpace(target.Env)
	service := strings.TrimSpace(target.Service)

	// etcd 下按前缀扫描同一服务的所有 lease 实例。
	prefix := fmt.Sprintf("%s/%s/%s", namespace, env, service)
	response, err := p.client.Get(queryCtx, prefix, clientv3.WithPrefix())
	if err != nil {
		return model.ServiceSnapshot{}, err
	}

	snapshot := model.ServiceSnapshot{
		Service: model.ServiceRef{
			// 快照里的服务维度统一以“本次查询目标 + provider 默认值”收敛而来。
			Service:   service,
			Namespace: namespace,
			Env:       env,
			Port:      target.Port,
		},
		Endpoints: make([]model.Endpoint, 0, len(response.Kvs)),
	}

	for _, kv := range response.Kvs {
		endpoint, ok := decodeEndpoint(kv)
		if !ok {
			// 单条坏数据只跳过，不影响其他健康实例继续参与解析。
			continue
		}
		snapshot.Endpoints = append(snapshot.Endpoints, endpoint)
	}

	if len(snapshot.Endpoints) == 0 {
		return model.ServiceSnapshot{}, fmt.Errorf("no healthy etcd endpoints found for service %s", service)
	}

	return snapshot, nil
}

func (p *Provider) Watch(ctx context.Context, target model.ServiceRef) (watchapi.Stream, error) {
	stream := newPollWatchStream()
	go func() {
		defer stream.Close()

		ticker := time.NewTicker(durationFromMS(p.Config.QueryTimeoutMS))
		defer ticker.Stop()

		var last model.ServiceSnapshot
		var hasLast bool
		poll := func() {
			snapshot, err := p.Resolve(ctx, target)
			if err != nil {
				if hasLast {
					hasLast = false
					last = model.ServiceSnapshot{}
					stream.publish(watchapi.Event{
						Kind:   watchapi.EventDelete,
						Target: target,
					})
				}
				return
			}
			if !hasLast || !reflect.DeepEqual(last, snapshot) {
				hasLast = true
				last = snapshot
				stream.publish(watchapi.Event{
					Kind:     watchapi.EventUpsert,
					Target:   snapshot.Service,
					Snapshot: snapshot,
				})
			}
		}

		poll()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				poll()
			}
		}
	}()
	return stream, nil
}

func (p *Provider) queryContext(parent context.Context) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		return context.WithCancel(parent)
	}
	timeout := durationFromMS(p.Config.QueryTimeoutMS)
	return context.WithTimeout(parent, timeout)
}

// decodeEndpoint 解析单条 etcd KV 并抽取可路由实例。
func decodeEndpoint(kv *mvccpb.KeyValue) (model.Endpoint, bool) {
	if kv == nil || len(kv.Value) == 0 {
		return model.Endpoint{}, false
	}

	var node serviceNode
	if err := json.Unmarshal(kv.Value, &node); err != nil {
		// 无法解析的注册值直接丢弃，避免把目录脏数据带入 dataplane。
		return model.Endpoint{}, false
	}
	if node.Network == nil || strings.TrimSpace(node.Network.Internal) == "" {
		// 没有可访问地址的注册值即使存在，也不能参与真实路由。
		return model.Endpoint{}, false
	}

	host, port, err := splitAddress(node.Network.Internal)
	if err != nil {
		// 地址格式不合法时直接视为坏数据。
		return model.Endpoint{}, false
	}

	weight := uint32(1)
	if node.Weight > 0 {
		// 未提供 weight 时回退为 1，保持最小可路由语义。
		weight = uint32(node.Weight)
	}

	return model.Endpoint{
		Address: host,
		Port:    uint32(port),
		Weight:  weight,
	}, true
}

// splitAddress 把 host:port 格式拆成内部 endpoint 字段。
func splitAddress(raw string) (string, uint16, error) {
	// net.SplitHostPort 能正确处理 IPv6 等更复杂地址格式。
	host, portRaw, err := net.SplitHostPort(raw)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		return "", 0, err
	}
	return host, uint16(port), nil
}

// durationFromMS 统一把毫秒值转换成 time.Duration，并提供默认值。
func durationFromMS(value uint64) time.Duration {
	if value == 0 {
		// 不显式配置时回退 1 秒，和其他连接型默认值保持一致。
		return time.Second
	}
	return time.Duration(value) * time.Millisecond
}

type pollWatchStream struct {
	events chan watchapi.Event
}

func newPollWatchStream() *pollWatchStream {
	return &pollWatchStream{
		events: make(chan watchapi.Event, 8),
	}
}

func (s *pollWatchStream) Events() <-chan watchapi.Event {
	return s.events
}

func (s *pollWatchStream) Close() error {
	defer func() {
		recover()
	}()
	close(s.events)
	return nil
}

func (s *pollWatchStream) publish(event watchapi.Event) {
	select {
	case s.events <- event:
	default:
	}
}
