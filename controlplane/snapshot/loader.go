package snapshot

import (
	"context"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source"
)

// Loader 负责把 source 快照拉到 controlplane store 中。
type Loader struct {
	store    *Store
	provider source.Provider
}

// NewLoader 创建一个最小 source -> controlplane store 同步器。
func NewLoader(store *Store, provider source.Provider) *Loader {
	return &Loader{
		store:    store,
		provider: provider,
	}
}

// Refresh 拉取单个目标服务的最新快照并写入本地 cache。
func (l *Loader) Refresh(ctx context.Context, target model.ServiceRef) (*controlv1.ServiceSnapshot, bool, error) {
	if l == nil || l.store == nil || l.provider == nil {
		return nil, false, nil
	}

	snapshot, err := l.provider.Resolve(ctx, target)
	if err != nil {
		return nil, false, err
	}

	protoSnapshot, changed := l.store.PutModelSnapshot(snapshot)
	return protoSnapshot, changed, nil
}

// RefreshMany 顺序刷新多个目标服务，返回本轮发生变化的快照。
func (l *Loader) RefreshMany(ctx context.Context, targets []model.ServiceRef) ([]*controlv1.ServiceSnapshot, error) {
	changed := make([]*controlv1.ServiceSnapshot, 0, len(targets))
	for _, target := range targets {
		snapshot, updated, err := l.Refresh(ctx, target)
		if err != nil {
			return nil, err
		}
		if updated && snapshot != nil {
			changed = append(changed, snapshot)
		}
	}
	return changed, nil
}
