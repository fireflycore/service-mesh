package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type Provider struct {
	mu        sync.RWMutex
	snapshots map[string]model.ServiceSnapshot
}

func New(snapshots map[string]model.ServiceSnapshot) *Provider {
	cloned := make(map[string]model.ServiceSnapshot, len(snapshots))
	for key, value := range snapshots {
		cloned[key] = value
	}
	return &Provider{snapshots: cloned}
}

func (p *Provider) Name() string {
	return "memory"
}

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

func serviceKey(target model.ServiceRef) string {
	return target.Namespace + "/" + target.Env + "/" + target.Service
}
