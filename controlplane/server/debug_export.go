package server

import (
	"sort"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type DataplaneIdentityExport struct {
	DataplaneID string
	NodeID      string
	Namespace   string
	Env         string
}

type SubscriberDebugExport struct {
	SubscriberID uint64
	Identity     DataplaneIdentityExport
	Targets      []model.ServiceRef
}

type ServerDebugStateExport struct {
	SubscriberCount   int
	TrackedTargetCount int
	Subscribers       []SubscriberDebugExport
	TrackedTargets    []model.ServiceRef
}

func sortServiceRefs(targets []model.ServiceRef) {
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Namespace != targets[j].Namespace {
			return targets[i].Namespace < targets[j].Namespace
		}
		if targets[i].Env != targets[j].Env {
			return targets[i].Env < targets[j].Env
		}
		if targets[i].Service != targets[j].Service {
			return targets[i].Service < targets[j].Service
		}
		return targets[i].Port < targets[j].Port
	})
}
