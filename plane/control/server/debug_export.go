package server

import (
	"sort"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
	controltelemetry "github.com/fireflycore/service-mesh/plane/control/telemetry"
)

// DataplaneIdentityExport documents the corresponding declaration.
type DataplaneIdentityExport struct {
	DataplaneID string
	NodeID      string
	Namespace   string
	Env         string
}

// SubscriberDebugExport documents the corresponding declaration.
type SubscriberDebugExport struct {
	SubscriberID uint64
	Identity     DataplaneIdentityExport
	Targets      []model.ServiceRef
}

// ServerDebugStateExport documents the corresponding declaration.
type ServerDebugStateExport struct {
	SubscriberCount    int
	TrackedTargetCount int
	SnapshotCount      int
	RoutePolicyCount   int
	Subscribers        []SubscriberDebugExport
	TrackedTargets     []model.ServiceRef
	Snapshots          []SnapshotDebugExport
	RoutePolicies      []RoutePolicyDebugExport
}

// SnapshotDebugExport documents the corresponding declaration.
type SnapshotDebugExport struct {
	Service     model.ServiceRef
	Status      string
	ReasonClass string
}

// RoutePolicyDebugExport documents the corresponding declaration.
type RoutePolicyDebugExport struct {
	Service model.ServiceRef
}

// sortServiceRefs documents the corresponding declaration.
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

// sortSnapshotExports documents the corresponding declaration.
func sortSnapshotExports(items []SnapshotDebugExport) {
	sort.Slice(items, func(i, j int) bool {
		return compareServiceRefs(items[i].Service, items[j].Service)
	})
}

// sortRoutePolicyExports documents the corresponding declaration.
func sortRoutePolicyExports(items []RoutePolicyDebugExport) {
	sort.Slice(items, func(i, j int) bool {
		return compareServiceRefs(items[i].Service, items[j].Service)
	})
}

// compareServiceRefs documents the corresponding declaration.
func compareServiceRefs(left, right model.ServiceRef) bool {
	if left.Namespace != right.Namespace {
		return left.Namespace < right.Namespace
	}
	if left.Env != right.Env {
		return left.Env < right.Env
	}
	if left.Service != right.Service {
		return left.Service < right.Service
	}
	return left.Port < right.Port
}

// exportSnapshot documents the corresponding declaration.
func exportSnapshot(snapshot *controlv1.ServiceSnapshot) SnapshotDebugExport {
	if snapshot == nil || snapshot.GetService() == nil {
		return SnapshotDebugExport{}
	}
	return SnapshotDebugExport{
		Service: model.ServiceRef{
			Service:   snapshot.GetService().GetService(),
			Namespace: snapshot.GetService().GetNamespace(),
			Env:       snapshot.GetService().GetEnv(),
			Port:      snapshot.GetService().GetPort(),
		},
		Status:      controltelemetry.SnapshotStatusLabel(snapshot.GetStatus()),
		ReasonClass: controltelemetry.SnapshotReasonClass(snapshot.GetStatusReason()),
	}
}

// exportRoutePolicy documents the corresponding declaration.
func exportRoutePolicy(policy *controlv1.RoutePolicy) RoutePolicyDebugExport {
	if policy == nil || policy.GetService() == nil {
		return RoutePolicyDebugExport{}
	}
	return RoutePolicyDebugExport{
		Service: model.ServiceRef{
			Service:   policy.GetService().GetService(),
			Namespace: policy.GetService().GetNamespace(),
			Env:       policy.GetService().GetEnv(),
			Port:      policy.GetService().GetPort(),
		},
	}
}
