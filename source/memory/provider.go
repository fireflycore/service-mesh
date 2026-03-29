package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// Provider 是一个纯内存目录源，主要给单元测试使用。
type Provider struct {
	mu sync.RWMutex
	// snapshots 直接按 serviceKey 保存测试预置的结果。
	snapshots map[string]model.ServiceSnapshot
}

// New 创建一个纯内存 provider。
//
// 这个实现主要用于单元测试：
// - 不依赖外部注册中心
// - 可以稳定构造各种服务目录场景
func New(snapshots map[string]model.ServiceSnapshot) *Provider {
	// clone 一份输入 map，避免测试调用方后续修改原 map 影响 provider 内部状态。
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
		// 测试中显式报“找不到快照”通常比返回空结果更容易定位问题。
		return model.ServiceSnapshot{}, fmt.Errorf("service snapshot not found: %s", key)
	}
	return snapshot, nil
}

// serviceKey 使用和其他 source 一致的 namespace/env/service 组合键。
func serviceKey(target model.ServiceRef) string {
	return target.Namespace + "/" + target.Env + "/" + target.Service
}
