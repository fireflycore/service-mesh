package consul

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/watch"
	"github.com/hashicorp/consul/api"
)

// healthService 抽象 Consul 健康查询能力，便于单元测试时替换。
type healthService interface {
	Service(service, tag string, passingOnly bool, q *api.QueryOptions) ([]*api.ServiceEntry, *api.QueryMeta, error)
}

type serviceResult struct {
	rows []*api.ServiceEntry
	err  error
}

// Provider 是第三版引入的真实 Consul source 实现。
//
// 它负责把 Consul 健康实例列表转换成 service-mesh 内部的 ServiceSnapshot。
type Provider struct {
	// Config 保留原始连接参数，方便测试和调试观察。
	Config config.ConsulSourceConfig
	// health 是对 Consul Health API 的最小依赖面。
	health healthService
}

// New 基于配置创建 Consul provider。
func New(cfg config.ConsulSourceConfig) (*Provider, error) {
	clientConfig := api.DefaultConfig()
	// Address 是 Consul API 的核心连接入口。
	clientConfig.Address = cfg.Address
	if strings.TrimSpace(cfg.Scheme) != "" {
		// 只有显式提供 scheme 时才覆盖默认值。
		clientConfig.Scheme = cfg.Scheme
	}
	if strings.TrimSpace(cfg.Token) != "" {
		// Token 只在启用了 ACL 的集群里需要。
		clientConfig.Token = cfg.Token
	}

	client, err := api.NewClient(clientConfig)
	if err != nil {
		return nil, err
	}

	return &Provider{
		Config: cfg,
		health: client.Health(),
	}, nil
}

func (p *Provider) Name() string {
	// Name 主要用于日志与调试展示。
	return "consul"
}

// Resolve 从 Consul 拉取健康实例，并转换成内部快照。
//
// 当前策略比较克制：
// - 只读取 passing 实例
// - service 名直接使用 target.Service
// - 如 Service.Address 为空，则回退 Node.Address
func (p *Provider) Resolve(ctx context.Context, target model.ServiceRef) (model.ServiceSnapshot, error) {
	queryCtx, cancel := p.queryContext(ctx)
	defer cancel()

	resultCh := make(chan serviceResult, 1)
	go func() {
		// 第三个参数 passingOnly=true，意味着只拿健康实例。
		rows, _, err := p.health.Service(target.Service, "", true, &api.QueryOptions{
			Datacenter: strings.TrimSpace(p.Config.Datacenter),
		})
		resultCh <- serviceResult{rows: rows, err: err}
	}()

	var rows []*api.ServiceEntry
	select {
	case <-queryCtx.Done():
		return model.ServiceSnapshot{}, fmt.Errorf("consul query timeout for service %s: %w", target.Service, queryCtx.Err())
	case result := <-resultCh:
		if result.err != nil {
			return model.ServiceSnapshot{}, result.err
		}
		rows = result.rows
	}

	snapshot := model.ServiceSnapshot{
		// Consul 当前不会额外改写 service 维度，直接沿用查询目标。
		Service:   target,
		Endpoints: make([]model.Endpoint, 0, len(rows)),
	}

	for _, row := range rows {
		endpoint, ok := decodeEndpoint(row)
		if !ok {
			// 非法实例直接跳过，避免单个脏节点拖垮整个解析结果。
			continue
		}
		snapshot.Endpoints = append(snapshot.Endpoints, endpoint)
	}

	if len(snapshot.Endpoints) == 0 {
		return model.ServiceSnapshot{}, fmt.Errorf("%w: consul service=%s", watch.ErrNoHealthyEndpoints, target.Service)
	}

	return snapshot, nil
}

func (p *Provider) Watch(ctx context.Context, target model.ServiceRef) (watch.Stream, error) {
	return watch.RunPollingWithOptions(ctx, durationFromQueryMS(p.Config.QueryTimeoutMS), target, watch.PollingOptions{
		DegradeAfterConsecutiveErrors: int(p.Config.WatchDegradeAfterErrors),
	}, func(ctx context.Context) (model.ServiceSnapshot, bool, error) {
		snapshot, err := p.Resolve(ctx, target)
		if err != nil {
			return model.ServiceSnapshot{}, false, err
		}
		return snapshot, true, nil
	}), nil
}

func (p *Provider) queryContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := time.Second
	if p.Config.QueryTimeoutMS > 0 {
		timeout = time.Duration(p.Config.QueryTimeoutMS) * time.Millisecond
	}
	if _, ok := parent.Deadline(); ok {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func durationFromQueryMS(value uint64) time.Duration {
	if value == 0 {
		return time.Second
	}
	return time.Duration(value) * time.Millisecond
}

// decodeEndpoint 从 Consul 的 ServiceEntry 中提取最小 endpoint 信息。
func decodeEndpoint(row *api.ServiceEntry) (model.Endpoint, bool) {
	if row == nil || row.Service == nil {
		return model.Endpoint{}, false
	}

	address := strings.TrimSpace(row.Service.Address)
	if address == "" && row.Node != nil {
		// 某些 Consul 注册只填 Node.Address，这里做一次回退兼容。
		address = strings.TrimSpace(row.Node.Address)
	}
	if address == "" || row.Service.Port == 0 {
		// 地址或端口任何一个缺失，都无法形成可路由 endpoint。
		return model.Endpoint{}, false
	}

	return model.Endpoint{
		Address: address,
		Port:    uint32(row.Service.Port),
		// 当前 Consul provider 先统一给 1，后续再视需要接入权重语义。
		Weight: 1,
	}, true
}
