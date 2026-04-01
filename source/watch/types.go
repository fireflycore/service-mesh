package watch

import (
	"context"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// EventKind 标记一次 watch 更新是新增/覆盖还是删除。
type EventKind string

const (
	// EventUpsert 表示目录源当前仍然有这个目标的可用快照。
	EventUpsert EventKind = "upsert"
	// EventDelete 表示目录源里已经不存在这个目标的快照。
	EventDelete EventKind = "delete"
)

// Event 是所有目录源统一输出的 watch 事件模型。
type Event struct {
	// Kind 用来告诉消费方这是 upsert 还是 delete。
	Kind EventKind
	// Target 标识这条事件对应的逻辑服务目标。
	Target model.ServiceRef
	// Snapshot 在 Kind 为 EventUpsert 时携带最新快照内容。
	Snapshot model.ServiceSnapshot
}

// Stream 把 provider 的 watch 能力暴露为基于 channel 的事件流。
type Stream interface {
	Events() <-chan Event
	Close() error
}

// Capable 表示一个 provider 具备主动 watch 目录变化的能力。
type Capable interface {
	Watch(ctx context.Context, target model.ServiceRef) (Stream, error)
}
