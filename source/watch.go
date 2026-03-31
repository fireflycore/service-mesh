package source

import (
	"github.com/fireflycore/service-mesh/source/watchapi"
)

type WatchEventKind = watchapi.EventKind

const (
	WatchEventUpsert WatchEventKind = watchapi.EventUpsert
	WatchEventDelete WatchEventKind = watchapi.EventDelete
)

type WatchEvent = watchapi.Event

type WatchStream = watchapi.Stream

type WatchCapable = watchapi.Capable
