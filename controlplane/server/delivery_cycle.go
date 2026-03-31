package server

import (
	"sort"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type deliveryCycle struct {
	cache               *arbitrationCache
	changedSnapshotKeys map[string]struct{}
	deliveredPolicyKeys map[string]struct{}
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
	if deleted := resp.GetServiceSnapshotDeleted(); deleted != nil {
		return true
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

func (d deliveryCycle) SubscribeBatch(subscriber *subscriber, targets []model.ServiceRef, changed []*controlv1.ServiceSnapshot) deliveryBatch {
	d.RememberChangedSnapshots(changed)

	builder := newDeliveryBatchBuilder(len(changed)+len(targets)*2, 0)
	for _, snapshot := range changed {
		builder.addStreamSnapshot(snapshot)
	}

	for _, target := range targets {
		if snapshot := d.SnapshotForSubscribeTarget(subscriber, target); snapshot != nil {
			builder.addStreamSnapshot(snapshot)
		}

		if policy := d.PolicyForSubscribeTarget(subscriber, target); policy != nil {
			builder.addStreamPolicy(policy)
		}
	}

	return builder.build()
}

func (d deliveryCycle) RegisterBatch(identity *controlv1.DataplaneIdentity) deliveryBatch {
	arbitrator := d.ForIdentity(identity)
	builder := newDeliveryBatchBuilder(len(arbitrator.SelectedSnapshots())+len(arbitrator.SelectedPolicies()), 0)
	builder.addStreamSnapshots(arbitrator.SelectedSnapshots())
	builder.addStreamPolicies(arbitrator.SelectedPolicies())
	return builder.build()
}

func (d deliveryCycle) TargetBroadcastBatch(subscribers map[uint64]*subscriber, resp *controlv1.ConnectResponse, target model.ServiceRef) deliveryBatch {
	if len(subscribers) == 0 || resp == nil {
		return deliveryBatch{}
	}

	builder := newDeliveryBatchBuilder(0, len(subscribers))
	for _, subscriber := range orderedSubscribers(subscribers) {
		if subscriber == nil || subscriber.pushCh == nil {
			continue
		}
		if !d.AllowsTargetResponse(subscriber, resp, target) {
			continue
		}
		builder.addPushResponse(subscriber.pushCh, resp)
	}
	return builder.build()
}

func (d deliveryCycle) ExplainTargetResponse(subscribers map[uint64]*subscriber, resp *controlv1.ConnectResponse, target model.ServiceRef) deliveryExplainSummary {
	summary := deliveryExplainSummary{
		responseKind: responseKind(resp),
		target:       target,
	}
	for _, subscriber := range orderedSubscribers(subscribers) {
		if subscriber == nil || subscriber.pushCh == nil {
			continue
		}
		match := evaluateSelectorMatch(selectorFromSubscriber(subscriber), selectorFromResponse(resp, target))
		trace := deliveryDecisionTrace{
			dataplaneID:       subscriberDataplaneID(subscriber),
			subscriptionMatch: match.subscriptionLabel(),
			identityMatch:     match.identityLabel(),
		}
		switch match.subscription {
		case matchPriorityExact:
			summary.subscriptionExact++
		case matchPriorityFallback:
			summary.subscriptionFallback++
		}
		switch match.identity {
		case matchPriorityExact:
			summary.identityExact++
		case matchPriorityFallback:
			summary.identityFallback++
		}
		if match.subscription == matchPriorityNone {
			summary.deniedSubscription++
			trace.decision = "denied_subscription"
			summary.trace = append(summary.trace, trace)
			continue
		}
		if match.identity == matchPriorityNone {
			summary.deniedIdentity++
			trace.decision = "denied_identity"
			summary.trace = append(summary.trace, trace)
			continue
		}
		if !d.AllowsResponse(subscriber, resp) {
			summary.deniedArbitration++
			trace.decision = "denied_arbitration"
			summary.trace = append(summary.trace, trace)
			continue
		}
		summary.delivered++
		trace.decision = "delivered"
		summary.trace = append(summary.trace, trace)
	}
	return summary
}

func (d deliveryCycle) AllowsTargetResponse(subscriber *subscriber, resp *controlv1.ConnectResponse, target model.ServiceRef) bool {
	if !matchesSelectors(selectorFromSubscriber(subscriber), selectorFromResponse(resp, target)) {
		return false
	}
	return d.AllowsResponse(subscriber, resp)
}

func subscriberDataplaneID(subscriber *subscriber) string {
	if subscriber == nil || subscriber.identity == nil {
		return ""
	}
	return subscriber.identity.GetDataplaneId()
}

func orderedSubscribers(subscribers map[uint64]*subscriber) []*subscriber {
	if len(subscribers) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(subscribers))
	for id := range subscribers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})
	ordered := make([]*subscriber, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, subscribers[id])
	}
	return ordered
}
