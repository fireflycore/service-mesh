package server

import (
	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"google.golang.org/grpc"
)

// plannedDelivery documents the corresponding declaration.
type plannedDelivery struct {
	pushCh   chan *controlv1.ConnectResponse
	response *controlv1.ConnectResponse
}

// deliveryBatch documents the corresponding declaration.
type deliveryBatch struct {
	streamResponses []*controlv1.ConnectResponse
	deliveries      []plannedDelivery
}

// deliveryBatchBuilder documents the corresponding declaration.
type deliveryBatchBuilder struct {
	batch deliveryBatch
}

// newDeliveryBatchBuilder documents the corresponding declaration.
func newDeliveryBatchBuilder(streamCapacity, deliveryCapacity int) *deliveryBatchBuilder {
	if streamCapacity < 0 {
		streamCapacity = 0
	}
	if deliveryCapacity < 0 {
		deliveryCapacity = 0
	}
	return &deliveryBatchBuilder{
		batch: deliveryBatch{
			streamResponses: make([]*controlv1.ConnectResponse, 0, streamCapacity),
			deliveries:      make([]plannedDelivery, 0, deliveryCapacity),
		},
	}
}

// addStreamResponse documents the corresponding declaration.
func (b *deliveryBatchBuilder) addStreamResponse(resp *controlv1.ConnectResponse) {
	if b == nil || resp == nil {
		return
	}
	b.batch.streamResponses = append(b.batch.streamResponses, resp)
}

// addStreamSnapshot documents the corresponding declaration.
func (b *deliveryBatchBuilder) addStreamSnapshot(snapshot *controlv1.ServiceSnapshot) {
	b.addStreamResponse(snapshotResponse(snapshot))
}

// addStreamPolicy documents the corresponding declaration.
func (b *deliveryBatchBuilder) addStreamPolicy(policy *controlv1.RoutePolicy) {
	b.addStreamResponse(routePolicyResponse(policy))
}

// addStreamSnapshotDeleted documents the corresponding declaration.
func (b *deliveryBatchBuilder) addStreamSnapshotDeleted(deleted *controlv1.ServiceSnapshotDeleted) {
	b.addStreamResponse(snapshotDeletedResponse(deleted))
}

// addStreamSnapshots documents the corresponding declaration.
func (b *deliveryBatchBuilder) addStreamSnapshots(snapshots []*controlv1.ServiceSnapshot) {
	for _, snapshot := range snapshots {
		b.addStreamSnapshot(snapshot)
	}
}

// addStreamPolicies documents the corresponding declaration.
func (b *deliveryBatchBuilder) addStreamPolicies(policies []*controlv1.RoutePolicy) {
	for _, policy := range policies {
		b.addStreamPolicy(policy)
	}
}

// addPushResponse documents the corresponding declaration.
func (b *deliveryBatchBuilder) addPushResponse(pushCh chan *controlv1.ConnectResponse, resp *controlv1.ConnectResponse) {
	if b == nil || pushCh == nil || resp == nil {
		return
	}
	b.batch.deliveries = append(b.batch.deliveries, plannedDelivery{
		pushCh:   pushCh,
		response: resp,
	})
}

// build documents the corresponding declaration.
func (b *deliveryBatchBuilder) build() deliveryBatch {
	if b == nil {
		return deliveryBatch{}
	}
	return b.batch
}

// Send documents the corresponding declaration.
func (b deliveryBatch) Send(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse]) error {
	for _, resp := range b.streamResponses {
		if resp == nil {
			continue
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
	return nil
}

// Push documents the corresponding declaration.
func (b deliveryBatch) Push() {
	for _, delivery := range b.deliveries {
		if delivery.pushCh == nil || delivery.response == nil {
			continue
		}
		select {
		case delivery.pushCh <- delivery.response:
		default:
		}
	}
}

// StreamCount documents the corresponding declaration.
func (b deliveryBatch) StreamCount() int {
	return len(b.streamResponses)
}

// DeliveryCount documents the corresponding declaration.
func (b deliveryBatch) DeliveryCount() int {
	return len(b.deliveries)
}

// Explain documents the corresponding declaration.
func (b deliveryBatch) Explain() batchExplainSummary {
	summary := batchExplainSummary{
		streamResponses: len(b.streamResponses),
	}
	for _, resp := range b.streamResponses {
		switch responseKind(resp) {
		case "service_snapshot":
			summary.serviceSnapshots++
		case "service_snapshot_deleted":
			summary.serviceSnapshotDeleted++
		case "route_policy":
			summary.routePolicies++
		default:
			summary.unknown++
		}
	}
	return summary
}

// snapshotResponse documents the corresponding declaration.
func snapshotResponse(snapshot *controlv1.ServiceSnapshot) *controlv1.ConnectResponse {
	if snapshot == nil {
		return nil
	}
	return &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_ServiceSnapshot{
			ServiceSnapshot: snapshot,
		},
	}
}

// routePolicyResponse documents the corresponding declaration.
func routePolicyResponse(policy *controlv1.RoutePolicy) *controlv1.ConnectResponse {
	if policy == nil {
		return nil
	}
	return &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_RoutePolicy{
			RoutePolicy: policy,
		},
	}
}

// snapshotDeletedResponse documents the corresponding declaration.
func snapshotDeletedResponse(deleted *controlv1.ServiceSnapshotDeleted) *controlv1.ConnectResponse {
	if deleted == nil {
		return nil
	}
	return &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_ServiceSnapshotDeleted{
			ServiceSnapshotDeleted: deleted,
		},
	}
}
