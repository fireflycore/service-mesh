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
	if resp == nil || resp.GetServiceSnapshot() == nil {
		return true
	}
	return d.AllowsSnapshot(subscriber, resp.GetServiceSnapshot())
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
