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

type deliveryDecisionTrace struct {
	dataplaneID       string
	subscriptionMatch string
	identityMatch     string
	decision          string
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
