package server

import (
	"fmt"
	"strings"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type deliveryExplainSummary struct {
	responseKind         string
	target               model.ServiceRef
	delivered            int
	deniedSubscription   int
	deniedIdentity       int
	deniedArbitration    int
	subscriptionExact    int
	subscriptionFallback int
	identityExact        int
	identityFallback     int
	trace                []deliveryDecisionTrace
}

type batchExplainSummary struct {
	streamResponses        int
	serviceSnapshots       int
	serviceSnapshotDeleted int
	routePolicies          int
	unknown                int
}

type replayExplainSummary struct {
	snapshotExact    int
	snapshotFallback int
	policyExact      int
	policyFallback   int
}

type ReplayExplainExport struct {
	SnapshotExact    int
	SnapshotFallback int
	PolicyExact      int
	PolicyFallback   int
}

type deliveryDecisionTrace struct {
	dataplaneID       string
	subscriptionMatch string
	identityMatch     string
	decision          string
}

type DeliveryDecisionTraceExport struct {
	DataplaneID       string
	SubscriptionMatch string
	IdentityMatch     string
	Decision          string
}

type DeliveryExplainExport struct {
	ResponseKind         string
	Delivered            int
	DeniedSubscription   int
	DeniedIdentity       int
	DeniedArbitration    int
	SubscriptionExact    int
	SubscriptionFallback int
	IdentityExact        int
	IdentityFallback     int
	TraceTotal           int
	TraceShown           int
	Trace                []DeliveryDecisionTraceExport
}

func (s deliveryExplainSummary) traceString(limit int) string {
	if len(s.trace) == 0 || limit == 0 {
		return ""
	}
	if limit < 0 || limit > len(s.trace) {
		limit = len(s.trace)
	}
	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		item := s.trace[i]
		parts = append(parts, fmt.Sprintf("%s:%s(subscription=%s,identity=%s)", item.dataplaneID, item.decision, item.subscriptionMatch, item.identityMatch))
	}
	return strings.Join(parts, ";")
}

func (s deliveryExplainSummary) traceShownCount(limit int) int {
	if len(s.trace) == 0 || limit == 0 {
		return 0
	}
	if limit < 0 || limit > len(s.trace) {
		return len(s.trace)
	}
	return limit
}

func (s deliveryExplainSummary) export(limit int) DeliveryExplainExport {
	shown := s.traceShownCount(limit)
	exported := DeliveryExplainExport{
		ResponseKind:         s.responseKind,
		Delivered:            s.delivered,
		DeniedSubscription:   s.deniedSubscription,
		DeniedIdentity:       s.deniedIdentity,
		DeniedArbitration:    s.deniedArbitration,
		SubscriptionExact:    s.subscriptionExact,
		SubscriptionFallback: s.subscriptionFallback,
		IdentityExact:        s.identityExact,
		IdentityFallback:     s.identityFallback,
		TraceTotal:           len(s.trace),
		TraceShown:           shown,
	}
	if shown == 0 {
		return exported
	}
	exported.Trace = make([]DeliveryDecisionTraceExport, 0, shown)
	for i := 0; i < shown; i++ {
		item := s.trace[i]
		exported.Trace = append(exported.Trace, DeliveryDecisionTraceExport{
			DataplaneID:       item.dataplaneID,
			SubscriptionMatch: item.subscriptionMatch,
			IdentityMatch:     item.identityMatch,
			Decision:          item.decision,
		})
	}
	return exported
}

func (s replayExplainSummary) export() ReplayExplainExport {
	return ReplayExplainExport{
		SnapshotExact:    s.snapshotExact,
		SnapshotFallback: s.snapshotFallback,
		PolicyExact:      s.policyExact,
		PolicyFallback:   s.policyFallback,
	}
}

func responseKind(resp *controlv1.ConnectResponse) string {
	switch {
	case resp == nil:
		return "nil"
	case resp.GetServiceSnapshot() != nil:
		return "service_snapshot"
	case resp.GetServiceSnapshotDeleted() != nil:
		return "service_snapshot_deleted"
	case resp.GetRoutePolicy() != nil:
		return "route_policy"
	default:
		return "unknown"
	}
}
