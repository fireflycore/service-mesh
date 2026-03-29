package consul

import (
	"context"
	"fmt"
	"strings"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/hashicorp/consul/api"
)

// healthService 抽象 Consul 健康查询能力，便于单元测试时替换。
type healthService interface {
	Service(service, tag string, passingOnly bool, q *api.QueryOptions) ([]*api.ServiceEntry, *api.QueryMeta, error)
}

// Provider 是第三版引入的真实 Consul source 实现。
//
// 它负责把 Consul 健康实例列表转换成 service-mesh 内部的 ServiceSnapshot。
type Provider struct {
	Config config.ConsulSourceConfig
	health healthService
}

// New 基于配置创建 Consul provider。
func New(cfg config.ConsulSourceConfig) (*Provider, error) {
	clientConfig := api.DefaultConfig()
	clientConfig.Address = cfg.Address
	if strings.TrimSpace(cfg.Scheme) != "" {
		clientConfig.Scheme = cfg.Scheme
	}
	if strings.TrimSpace(cfg.Token) != "" {
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
	return "consul"
}

// Resolve 从 Consul 拉取健康实例，并转换成内部快照。
//
// 当前策略比较克制：
// - 只读取 passing 实例
// - service 名直接使用 target.Service
// - 如 Service.Address 为空，则回退 Node.Address
func (p *Provider) Resolve(_ context.Context, target model.ServiceRef) (model.ServiceSnapshot, error) {
	rows, _, err := p.health.Service(target.Service, "", true, &api.QueryOptions{
		Datacenter: strings.TrimSpace(p.Config.Datacenter),
	})
	if err != nil {
		return model.ServiceSnapshot{}, err
	}

	snapshot := model.ServiceSnapshot{
		Service:   target,
		Endpoints: make([]model.Endpoint, 0, len(rows)),
	}

	for _, row := range rows {
		endpoint, ok := decodeEndpoint(row)
		if !ok {
			continue
		}
		snapshot.Endpoints = append(snapshot.Endpoints, endpoint)
	}

	if len(snapshot.Endpoints) == 0 {
		return model.ServiceSnapshot{}, fmt.Errorf("no healthy consul endpoints found for service %s", target.Service)
	}

	return snapshot, nil
}

// decodeEndpoint 从 Consul 的 ServiceEntry 中提取最小 endpoint 信息。
func decodeEndpoint(row *api.ServiceEntry) (model.Endpoint, bool) {
	if row == nil || row.Service == nil {
		return model.Endpoint{}, false
	}

	address := strings.TrimSpace(row.Service.Address)
	if address == "" && row.Node != nil {
		address = strings.TrimSpace(row.Node.Address)
	}
	if address == "" || row.Service.Port == 0 {
		return model.Endpoint{}, false
	}

	return model.Endpoint{
		Address: address,
		Port:    uint32(row.Service.Port),
		Weight:  1,
	}, true
}
