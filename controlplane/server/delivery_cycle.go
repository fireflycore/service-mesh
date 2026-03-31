package server

import (
	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type deliveryCycle struct {
	cache               *arbitrationCache
	changedSnapshotKeys map[string]struct{}
	deliveredPolicyKeys map[string]struct{}
}

type plannedDelivery struct {
	pushCh   chan *controlv1.ConnectResponse
	response *controlv1.ConnectResponse
}

type deliveryBatch struct {
	streamResponses []*controlv1.ConnectResponse
	deliveries      []plannedDelivery
}

func newDeliveryBatch(streamCapacity, deliveryCapacity int) deliveryBatch {
	if streamCapacity < 0 {
		streamCapacity = 0
	}
	if deliveryCapacity < 0 {
		deliveryCapacity = 0
	}
	return deliveryBatch{
		streamResponses: make([]*controlv1.ConnectResponse, 0, streamCapacity),
		deliveries:      make([]plannedDelivery, 0, deliveryCapacity),
	}
}

func (b *deliveryBatch) addStreamResponse(resp *controlv1.ConnectResponse) {
	if b == nil || resp == nil {
		return
	}
	b.streamResponses = append(b.streamResponses, resp)
}

func (b *deliveryBatch) addStreamSnapshot(snapshot *controlv1.ServiceSnapshot) {
	if snapshot == nil {
		return
	}
	b.addStreamResponse(snapshotResponse(snapshot))
}

func (b *deliveryBatch) addStreamPolicy(policy *controlv1.RoutePolicy) {
	if policy == nil {
		return
	}
	b.addStreamResponse(routePolicyResponse(policy))
}

func (b *deliveryBatch) addDeliveries(deliveries []plannedDelivery) {
	if b == nil || len(deliveries) == 0 {
		return
	}
	b.deliveries = append(b.deliveries, deliveries...)
}

func (b *deliveryBatch) addPlannedDelivery(pushCh chan *controlv1.ConnectResponse, resp *controlv1.ConnectResponse) {
	if b == nil || pushCh == nil || resp == nil {
		return
	}
	b.deliveries = append(b.deliveries, plannedDelivery{
		pushCh:   pushCh,
		response: resp,
	})
}

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

func newDeliveryCycle(store *snapshot.Store) deliveryCycle {
	if store == nil {
		return deliveryCycle{
			cache:               newArbitrationCache(nil, nil),
			changedSnapshotKeys: make(map[string]struct{}),
			deliveredPolicyKeys: make(map[string]struct{}),
		}
	}
	snapshots := store.AllServiceSnapshots()
	policies := store.AllRoutePolicies()
	return deliveryCycle{
		cache:               newArbitrationCache(snapshots, policies),
		changedSnapshotKeys: make(map[string]struct{}),
		deliveredPolicyKeys: make(map[string]struct{}),
	}
}

func (d deliveryCycle) ForIdentity(identity *controlv1.DataplaneIdentity) resourceArbitrator {
	if d.cache == nil {
		return resourceArbitrator{}
	}
	return d.cache.ForIdentity(identity)
}

func (d deliveryCycle) ForSubscriber(subscriber *subscriber) resourceArbitrator {
	if d.cache == nil {
		return resourceArbitrator{}
	}
	return d.cache.ForSubscriber(subscriber)
}

func (d deliveryCycle) AllowsPolicy(subscriber *subscriber, policy *controlv1.RoutePolicy) bool {
	return d.ForSubscriber(subscriber).AllowsPolicy(policy)
}

func (d deliveryCycle) AllowsSnapshot(subscriber *subscriber, snapshot *controlv1.ServiceSnapshot) bool {
	return d.ForSubscriber(subscriber).AllowsSnapshot(snapshot)
}

func (d deliveryCycle) AllowsResponse(subscriber *subscriber, resp *controlv1.ConnectResponse) bool {
	if resp == nil {
		return true
	}
	if snapshot := resp.GetServiceSnapshot(); snapshot != nil {
		return d.AllowsSnapshot(subscriber, snapshot)
	}
	if policy := resp.GetRoutePolicy(); policy != nil {
		return d.AllowsPolicy(subscriber, policy)
	}
	return true
}

func (d deliveryCycle) SnapshotForSubscriberTarget(subscriber *subscriber, target model.ServiceRef) *controlv1.ServiceSnapshot {
	snapshot := d.ForSubscriber(subscriber).SnapshotForTarget(target)
	if snapshot == nil {
		return nil
	}
	if !matchesSelectors(selectorFromSubscriber(subscriber), selectorFromSnapshot(snapshot, target, true)) {
		return nil
	}
	return snapshot
}

func (d deliveryCycle) PolicyForSubscriberTarget(subscriber *subscriber, target model.ServiceRef) *controlv1.RoutePolicy {
	policy := d.ForSubscriber(subscriber).PolicyForTarget(target)
	if policy == nil {
		return nil
	}
	if !matchesSelectors(selectorFromSubscriber(subscriber), selectorFromRoutePolicy(policy, target, true)) {
		return nil
	}
	return policy
}

func (d deliveryCycle) RememberChangedSnapshot(snapshot *controlv1.ServiceSnapshot) {
	if snapshot == nil || snapshot.GetService() == nil {
		return
	}
	d.changedSnapshotKeys[targetKey(model.ServiceRef{
		Service:   snapshot.GetService().GetService(),
		Namespace: snapshot.GetService().GetNamespace(),
		Env:       snapshot.GetService().GetEnv(),
		Port:      snapshot.GetService().GetPort(),
	})] = struct{}{}
}

func (d deliveryCycle) HasChangedSnapshotForTarget(target model.ServiceRef) bool {
	_, ok := d.changedSnapshotKeys[targetKey(target)]
	return ok
}

func (d deliveryCycle) SnapshotForSubscribeTarget(subscriber *subscriber, target model.ServiceRef) *controlv1.ServiceSnapshot {
	if d.HasChangedSnapshotForTarget(target) {
		return nil
	}
	return d.SnapshotForSubscriberTarget(subscriber, target)
}

func (d deliveryCycle) PolicyForSubscribeTarget(subscriber *subscriber, target model.ServiceRef) *controlv1.RoutePolicy {
	policy := d.PolicyForSubscriberTarget(subscriber, target)
	if policy == nil {
		return nil
	}
	key := targetKey(model.ServiceRef{
		Service:   policy.GetService().GetService(),
		Namespace: policy.GetService().GetNamespace(),
		Env:       policy.GetService().GetEnv(),
		Port:      policy.GetService().GetPort(),
	})
	if _, ok := d.deliveredPolicyKeys[key]; ok {
		return nil
	}
	d.deliveredPolicyKeys[key] = struct{}{}
	return policy
}

func (d deliveryCycle) RememberChangedSnapshots(changed []*controlv1.ServiceSnapshot) {
	for _, snapshot := range changed {
		d.RememberChangedSnapshot(snapshot)
	}
}

func (d deliveryCycle) SubscribeResponses(subscriber *subscriber, targets []model.ServiceRef, changed []*controlv1.ServiceSnapshot) []*controlv1.ConnectResponse {
	d.RememberChangedSnapshots(changed)

	batch := newDeliveryBatch(len(changed)+len(targets)*2, 0)
	for _, snapshot := range changed {
		batch.addStreamSnapshot(snapshot)
	}

	for _, target := range targets {
		if snapshot := d.SnapshotForSubscribeTarget(subscriber, target); snapshot != nil {
			batch.addStreamSnapshot(snapshot)
		}

		if policy := d.PolicyForSubscribeTarget(subscriber, target); policy != nil {
			batch.addStreamPolicy(policy)
		}
	}

	return batch.streamResponses
}

func (d deliveryCycle) RegisterBatch(identity *controlv1.DataplaneIdentity) deliveryBatch {
	arbitrator := d.ForIdentity(identity)
	batch := newDeliveryBatch(len(arbitrator.SelectedSnapshots())+len(arbitrator.SelectedPolicies()), 0)
	for _, serviceSnapshot := range arbitrator.SelectedSnapshots() {
		batch.addStreamSnapshot(serviceSnapshot)
	}
	for _, routePolicy := range arbitrator.SelectedPolicies() {
		batch.addStreamPolicy(routePolicy)
	}
	return batch
}

func (d deliveryCycle) SubscribeBatch(subscriber *subscriber, targets []model.ServiceRef, changed []*controlv1.ServiceSnapshot) deliveryBatch {
	return deliveryBatch{
		streamResponses: d.SubscribeResponses(subscriber, targets, changed),
	}
}

func (d deliveryCycle) PlanTargetResponse(subscribers map[uint64]*subscriber, resp *controlv1.ConnectResponse, target model.ServiceRef) []plannedDelivery {
	if len(subscribers) == 0 || resp == nil {
		return nil
	}

	batch := newDeliveryBatch(0, len(subscribers))
	for _, subscriber := range subscribers {
		if subscriber == nil || subscriber.pushCh == nil {
			continue
		}
		if !d.AllowsTargetResponse(subscriber, resp, target) {
			continue
		}
		batch.addPlannedDelivery(subscriber.pushCh, resp)
	}
	return batch.deliveries
}

func (d deliveryCycle) TargetBroadcastBatch(subscribers map[uint64]*subscriber, resp *controlv1.ConnectResponse, target model.ServiceRef) deliveryBatch {
	return deliveryBatch{
		deliveries: d.PlanTargetResponse(subscribers, resp, target),
	}
}

func (d deliveryCycle) AllowsTargetResponse(subscriber *subscriber, resp *controlv1.ConnectResponse, target model.ServiceRef) bool {
	if !matchesSelectors(selectorFromSubscriber(subscriber), selectorFromResponse(resp, target)) {
		return false
	}
	return d.AllowsResponse(subscriber, resp)
}

func (d deliveryCycle) AllowsTargetPolicy(subscriber *subscriber, policy *controlv1.RoutePolicy, target model.ServiceRef) bool {
	if !matchesSelectors(selectorFromSubscriber(subscriber), selectorFromRoutePolicy(policy, target, true)) {
		return false
	}
	return d.AllowsPolicy(subscriber, policy)
}
