package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/watchapi"
)

// Provider 是一个纯内存目录源，主要给单元测试使用。
type Provider struct {
	mu sync.RWMutex
	// snapshots 直接按 serviceKey 保存测试预置的结果。
	snapshots map[string]model.ServiceSnapshot
	watchers  map[string]map[uint64]chan watchapi.Event
	nextWatch uint64
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
	return &Provider{
		snapshots: cloned,
		watchers:  make(map[string]map[uint64]chan watchapi.Event),
	}
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

func (p *Provider) Watch(ctx context.Context, target model.ServiceRef) (watchapi.Stream, error) {
	key := serviceKey(target)

	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextWatch++
	id := p.nextWatch
	if p.watchers[key] == nil {
		p.watchers[key] = make(map[uint64]chan watchapi.Event)
	}
	ch := make(chan watchapi.Event, 8)
	p.watchers[key][id] = ch

	stream := &watchStream{
		events: ch,
		close: func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			watchers := p.watchers[key]
			if watchers == nil {
				return
			}
			if existing, ok := watchers[id]; ok {
				delete(watchers, id)
				close(existing)
			}
			if len(watchers) == 0 {
				delete(p.watchers, key)
			}
		},
	}

	go func() {
		<-ctx.Done()
		_ = stream.Close()
	}()

	return stream, nil
}

func (p *Provider) Upsert(snapshot model.ServiceSnapshot) {
	key := serviceKey(snapshot.Service)

	p.mu.Lock()
	p.snapshots[key] = snapshot
	watchers := cloneWatchers(p.watchers[key])
	p.mu.Unlock()

	event := watchapi.Event{
		Kind:     watchapi.EventUpsert,
		Target:   snapshot.Service,
		Snapshot: snapshot,
	}
	for _, ch := range watchers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (p *Provider) Delete(target model.ServiceRef) {
	key := serviceKey(target)

	p.mu.Lock()
	delete(p.snapshots, key)
	watchers := cloneWatchers(p.watchers[key])
	p.mu.Unlock()

	event := watchapi.Event{
		Kind:   watchapi.EventDelete,
		Target: target,
	}
	for _, ch := range watchers {
		select {
		case ch <- event:
		default:
		}
	}
}

// serviceKey 使用和其他 source 一致的 namespace/env/service 组合键。
func serviceKey(target model.ServiceRef) string {
	return target.Namespace + "/" + target.Env + "/" + target.Service
}

type watchStream struct {
	events chan watchapi.Event
	once   sync.Once
	close  func()
}

func (s *watchStream) Events() <-chan watchapi.Event {
	return s.events
}

func (s *watchStream) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		if s.close != nil {
			s.close()
		}
	})
	return nil
}

func cloneWatchers(watchers map[uint64]chan watchapi.Event) []chan watchapi.Event {
	result := make([]chan watchapi.Event, 0, len(watchers))
	for _, ch := range watchers {
		if ch == nil {
			continue
		}
		result = append(result, ch)
	}
	return result
}
