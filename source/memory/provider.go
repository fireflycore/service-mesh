package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// Provider 是一个纯内存目录源，主要给单元测试使用。
type Provider struct {
	mu        sync.RWMutex
	snapshots map[string]model.ServiceSnapshot
}

// New 创建一个纯内存 provider。
//
// 这个实现主要用于单元测试：
// - 不依赖外部注册中心
// - 可以稳定构造各种服务目录场景
func New(snapshots map[string]model.ServiceSnapshot) *Provider {
	cloned := make(map[string]model.ServiceSnapshot, len(snapshots))
	for key, value := range snapshots {
		cloned[key] = value
	}
	return &Provider{snapshots: cloned}
}

// Name 返回 provider 标识。
func (p *Provider) Name() string {
	return "memory"
}

// Resolve 从内存快照里直接返回目标服务。
func (p *Provider) Resolve(_ context.Context, target model.ServiceRef) (model.ServiceSnapshot, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	key := serviceKey(target)
	snapshot, ok := p.snapshots[key]
	if !ok {
		return model.ServiceSnapshot{}, fmt.Errorf("service snapshot not found: %s", key)
	}
	return snapshot, nil
}

// serviceKey 使用和其他 source 一致的 namespace/env/service 组合键。
func serviceKey(target model.ServiceRef) string {
	return target.Namespace + "/" + target.Env + "/" + target.Service
}
