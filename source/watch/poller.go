package watch

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// SnapshotPoller 定义一次轮询取数函数。
//
// 返回值中的 bool 用来表示目标当前是否仍然存在于目录源中。
type SnapshotPoller func(context.Context) (model.ServiceSnapshot, bool, error)

const (
	defaultDegradeAfterConsecutiveErrors = 3
	ErrorClassNotFound                   = "not_found"
	ErrorClassEmpty                      = "empty"
	ErrorClassTimeout                    = "timeout"
	ErrorClassUnavailable                = "unavailable"
	ErrorClassInternal                   = "internal"
)

// PollingOptions 定义 polling watch runner 的运行参数。
type PollingOptions struct {
	DegradeAfterConsecutiveErrors int
}

// RunPolling 使用默认参数启动一个 polling watch 事件流。
func RunPolling(ctx context.Context, interval time.Duration, target model.ServiceRef, poll SnapshotPoller) Stream {
	return RunPollingWithOptions(ctx, interval, target, PollingOptions{}, poll)
}

// RunPollingWithOptions 启动轮询循环，并把轮询结果转换成统一的 watch 事件。
func RunPollingWithOptions(ctx context.Context, interval time.Duration, target model.ServiceRef, options PollingOptions, poll SnapshotPoller) Stream {
	if interval <= 0 {
		interval = time.Second
	}
	degradeAfter := options.DegradeAfterConsecutiveErrors
	if degradeAfter <= 0 {
		degradeAfter = defaultDegradeAfterConsecutiveErrors
	}

	stream := newPollingStream()
	go func() {
		defer stream.Close()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var current model.ServiceSnapshot
		var hasCurrent bool
		var emitted model.ServiceSnapshot
		var hasEmitted bool
		consecutiveErrors := 0
		run := func() {
			snapshot, found, err := poll(ctx)
			if err != nil {
				consecutiveErrors++
				reason := formatPollingErrorReason(err)
				if consecutiveErrors >= degradeAfter {
					degraded := current
					if !hasCurrent {
						degraded = model.ServiceSnapshot{Service: target}
					}
					degraded.Status = model.SnapshotStatusDegraded
					degraded.StatusReason = reason
					if !hasEmitted || !reflect.DeepEqual(emitted, degraded) {
						emitted = degraded
						hasEmitted = true
						stream.publish(Event{
							Kind:     EventUpsert,
							Target:   degraded.Service,
							Snapshot: degraded,
						})
					}
					return
				}
				if hasCurrent {
					stale := current
					stale.Status = model.SnapshotStatusStale
					stale.StatusReason = reason
					if !hasEmitted || !reflect.DeepEqual(emitted, stale) {
						emitted = stale
						hasEmitted = true
						stream.publish(Event{
							Kind:     EventUpsert,
							Target:   stale.Service,
							Snapshot: stale,
						})
					}
				}
				return
			}
			consecutiveErrors = 0
			if !found {
				if hasEmitted {
					hasCurrent = false
					current = model.ServiceSnapshot{}
					emitted = model.ServiceSnapshot{}
					hasEmitted = false
					stream.publish(Event{
						Kind:   EventDelete,
						Target: target,
					})
				}
				return
			}
			current = snapshot
			hasCurrent = true
			if snapshot.Status == "" {
				snapshot.Status = model.SnapshotStatusCurrent
			}
			snapshot.StatusReason = ""
			if !hasEmitted || !reflect.DeepEqual(emitted, snapshot) {
				emitted = snapshot
				hasEmitted = true
				stream.publish(Event{
					Kind:     EventUpsert,
					Target:   snapshot.Service,
					Snapshot: snapshot,
				})
			}
		}

		run()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()

	return stream
}

// formatPollingErrorReason 把错误格式化成挂在 stale/degraded 快照上的稳定原因字符串。
func formatPollingErrorReason(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("class=%s error=%s", classifyPollingError(err), strings.TrimSpace(err.Error()))
}

// classifyPollingError 把 provider 错误收敛为有限的原因分类。
func classifyPollingError(err error) string {
	switch {
	case err == nil:
		return ErrorClassInternal
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorClassTimeout
	case errors.Is(err, context.Canceled):
		return ErrorClassUnavailable
	case errors.Is(err, ErrNoHealthyEndpoints):
		return ErrorClassEmpty
	default:
		return ErrorClassUnavailable
	}
}

// pollingStream 是 Stream 的最小内存实现。
type pollingStream struct {
	events chan Event
}

// newPollingStream 创建一个带缓冲的事件流，避免短时突发事件阻塞轮询循环。
func newPollingStream() *pollingStream {
	return &pollingStream{
		events: make(chan Event, 8),
	}
}

// Events 返回供 watch 消费方读取的事件通道。
func (s *pollingStream) Events() <-chan Event {
	return s.events
}

// Close 关闭事件通道，并把重复关闭视为无副作用操作。
func (s *pollingStream) Close() error {
	defer func() {
		recover()
	}()
	close(s.events)
	return nil
}

// publish 在缓冲区仍有空间时把事件写入通道。
func (s *pollingStream) publish(event Event) {
	select {
	case s.events <- event:
	default:
	}
}
