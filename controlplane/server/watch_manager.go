package server

import (
	"context"
	"strings"
	"sync"

	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type watchManager struct {
	loader   *snapshot.Loader
	onUpdate func(snapshot.WatchUpdate)

	mu      sync.Mutex
	rootCtx context.Context
	active  map[string]context.CancelFunc
}

func newWatchManager(loader *snapshot.Loader, onUpdate func(snapshot.WatchUpdate)) *watchManager {
	return &watchManager{
		loader:   loader,
		onUpdate: onUpdate,
		active:   make(map[string]context.CancelFunc),
	}
}

func (m *watchManager) Start(ctx context.Context, targets []model.ServiceRef) {
	if m == nil || ctx == nil {
		return
	}

	m.mu.Lock()
	m.rootCtx = ctx
	m.mu.Unlock()

	for _, target := range targets {
		m.Track(target)
	}
}

func (m *watchManager) Track(target model.ServiceRef) {
	if m == nil || m.loader == nil || strings.TrimSpace(target.Service) == "" {
		return
	}

	key := targetKey(target)

	m.mu.Lock()
	if _, ok := m.active[key]; ok {
		m.mu.Unlock()
		return
	}
	if m.rootCtx == nil {
		m.mu.Unlock()
		return
	}
	watchCtx, cancel := context.WithCancel(m.rootCtx)
	m.active[key] = cancel
	m.mu.Unlock()

	updates, err := m.loader.Watch(watchCtx, target)
	if err != nil || updates == nil {
		cancel()
		m.mu.Lock()
		delete(m.active, key)
		m.mu.Unlock()
		return
	}

	go func() {
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.active, key)
			m.mu.Unlock()
		}()

		for {
			select {
			case <-watchCtx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				if m.onUpdate != nil {
					m.onUpdate(update)
				}
			}
		}
	}()
}
