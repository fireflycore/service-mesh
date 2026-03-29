package etcd

import (
	"context"
	"errors"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type Provider struct {
	Config config.EtcdSourceConfig
}

// New 创建 Etcd provider。
//
// 当前第三版仍未实现真实 etcd 目录解析，所以这里只保留结构与配置入口。
func New(cfg config.EtcdSourceConfig) *Provider {
	return &Provider{Config: cfg}
}

func (p *Provider) Name() string {
	return "etcd"
}

// Resolve 目前仍是占位实现。
func (p *Provider) Resolve(_ context.Context, _ model.ServiceRef) (model.ServiceSnapshot, error) {
	return model.ServiceSnapshot{}, errors.New("etcd source resolve is not implemented")
}
