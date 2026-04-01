package source

import (
	"github.com/fireflycore/service-mesh/source/watch"
)

// WatchEventKind 为 source 门面层重新导出具体的 watch 事件类型。
type WatchEventKind = watch.EventKind

const (
	// WatchEventUpsert 表示目录源发出了一条新增或刷新后的快照事件。
	WatchEventUpsert WatchEventKind = watch.EventUpsert
	// WatchEventDelete 表示目录源移除了目标对应的快照。
	WatchEventDelete WatchEventKind = watch.EventDelete
)

// WatchEvent 为 source 门面层重新导出统一的 watch 事件模型。
type WatchEvent = watch.Event

// WatchStream 为 source 门面层重新导出通用的 watch 事件流约定。
type WatchStream = watch.Stream

// WatchCapable 为 source 门面层重新导出具备 watch 能力的 provider 接口。
type WatchCapable = watch.Capable
