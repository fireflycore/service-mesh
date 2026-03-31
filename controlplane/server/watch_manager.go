package server

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	controltelemetry "github.com/fireflycore/service-mesh/controlplane/telemetry"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type watchManager struct {
	loader   *snapshot.Loader
	telemetry *controltelemetry.Emitter
	onUpdate func(snapshot.WatchUpdate)

	mu      sync.Mutex
	rootCtx context.Context
	active  map[string]context.CancelFunc
}

const watchRestartBackoff = 200 * time.Millisecond

func newWatchManager(loader *snapshot.Loader, telemetry *controltelemetry.Emitter, onUpdate func(snapshot.WatchUpdate)) *watchManager {
	return &watchManager{
		loader:    loader,
		telemetry: telemetry,
		onUpdate:  onUpdate,
		active:    make(map[string]context.CancelFunc),
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
	if err != nil {
		updates = nil
	}

	go func() {
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.active, key)
			m.mu.Unlock()
		}()

		currentUpdates := updates
		for watchCtx.Err() == nil {
			if currentUpdates == nil {
				if !waitWatchRestart(watchCtx) {
					return
				}
				var watchErr error
				currentUpdates, watchErr = m.loader.Watch(watchCtx, target)
				if watchErr != nil {
					currentUpdates = nil
				}
				if currentUpdates == nil {
					continue
				}
			}

			restart := false
			for {
				select {
				case <-watchCtx.Done():
					return
				case update, ok := <-currentUpdates:
					if !ok {
						restart = true
						currentUpdates = nil
						break
					}
					if m.onUpdate != nil {
						m.onUpdate(update)
					}
				}
				if restart {
					break
				}
			}
			if !restart {
				continue
			}
			if m.telemetry != nil {
				m.telemetry.RecordWatchRestart(watchCtx, target.Service)
			}
			slog.Warn("controlplane watch restarting",
				slog.String("service", target.Service),
				slog.String("namespace", target.Namespace),
				slog.String("env", target.Env),
			)
			if !waitWatchRestart(watchCtx) {
				return
			}
			var watchErr error
			currentUpdates, watchErr = m.loader.Watch(watchCtx, target)
			if watchErr != nil {
				currentUpdates = nil
			}
		}
	}()
}

func waitWatchRestart(ctx context.Context) bool {
	timer := time.NewTimer(watchRestartBackoff)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
