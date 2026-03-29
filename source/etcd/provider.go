package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
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
	Weight  int          `json:"weight"`
	Network *networkInfo `json:"network"`
	Meta    *metaInfo    `json:"meta"`
}

type networkInfo struct {
	Internal string `json:"internal"`
}

type metaInfo struct {
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
	namespace := strings.TrimSpace(target.Namespace)
	if namespace == "" {
		// 如果调用方没写 namespace，则回退到 provider 自己的默认前缀。
		namespace = strings.TrimSpace(p.Config.Namespace)
	}
	env := strings.TrimSpace(target.Env)
	service := strings.TrimSpace(target.Service)

	// etcd 下按前缀扫描同一服务的所有 lease 实例。
	prefix := fmt.Sprintf("%s/%s/%s", namespace, env, service)
	response, err := p.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return model.ServiceSnapshot{}, err
	}

	snapshot := model.ServiceSnapshot{
		Service: model.ServiceRef{
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
		return model.Endpoint{}, false
	}

	host, port, err := splitAddress(node.Network.Internal)
	if err != nil {
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
		return time.Second
	}
	return time.Duration(value) * time.Millisecond
}
