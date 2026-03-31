package server

import (
	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type deliveryExplainSummary struct {
	responseKind       string
	target             model.ServiceRef
	delivered          int
	deniedSubscription int
	deniedIdentity     int
	deniedArbitration  int
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
