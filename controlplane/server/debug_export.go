package server

import (
	"sort"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	controltelemetry "github.com/fireflycore/service-mesh/controlplane/telemetry"
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
	SubscriberCount    int
	TrackedTargetCount int
	SnapshotCount      int
	RoutePolicyCount   int
	Subscribers        []SubscriberDebugExport
	TrackedTargets     []model.ServiceRef
	Snapshots          []SnapshotDebugExport
	RoutePolicies      []RoutePolicyDebugExport
}

type SnapshotDebugExport struct {
	Service     model.ServiceRef
	Status      string
	ReasonClass string
}

type RoutePolicyDebugExport struct {
	Service model.ServiceRef
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

func sortSnapshotExports(items []SnapshotDebugExport) {
	sort.Slice(items, func(i, j int) bool {
		return compareServiceRefs(items[i].Service, items[j].Service)
	})
}

func sortRoutePolicyExports(items []RoutePolicyDebugExport) {
	sort.Slice(items, func(i, j int) bool {
		return compareServiceRefs(items[i].Service, items[j].Service)
	})
}

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
