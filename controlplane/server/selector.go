package server

import (
	"strings"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type subscriberSelector struct {
	identity *controlv1.DataplaneIdentity
	targets  map[string]model.ServiceRef
}

type resourceSelector struct {
	target              model.ServiceRef
	service             *controlv1.ServiceRef
	requireSubscription bool
	requireIdentity     bool
}

type matchPriority uint8

const (
	matchPriorityNone matchPriority = iota
	matchPriorityFallback
	matchPriorityExact
)

type selectorMatch struct {
	subscription matchPriority
	identity     matchPriority
}

type resourceArbitrator struct {
	snapshots map[string]*controlv1.ServiceSnapshot
	policies  map[string]*controlv1.RoutePolicy
}

type arbitrationCache struct {
	snapshots []*controlv1.ServiceSnapshot
	policies  []*controlv1.RoutePolicy
	byKey     map[string]resourceArbitrator
}

func matchesDataplaneIdentity(service *controlv1.ServiceRef, identity *controlv1.DataplaneIdentity) bool {
	return matchIdentityScope(service, identity) != matchPriorityNone
}

func matchesIdentityScope(service *controlv1.ServiceRef, identity *controlv1.DataplaneIdentity) bool {
	return matchIdentityScope(service, identity) != matchPriorityNone
}

func matchesDimension(serviceValue, identityValue string) bool {
	return matchDimension(serviceValue, identityValue) != matchPriorityNone
}

func matchDimension(serviceValue, identityValue string) matchPriority {
	serviceValue = strings.TrimSpace(serviceValue)
	identityValue = strings.TrimSpace(identityValue)
	switch {
	case serviceValue == "" || identityValue == "":
		return matchPriorityFallback
	case serviceValue == identityValue:
		return matchPriorityExact
	default:
		return matchPriorityNone
	}
}

func matchIdentityScope(service *controlv1.ServiceRef, identity *controlv1.DataplaneIdentity) matchPriority {
	if service == nil || identity == nil {
		return matchPriorityFallback
	}
	namespace := matchDimension(service.GetNamespace(), identity.GetNamespace())
	if namespace == matchPriorityNone {
		return matchPriorityNone
	}
	env := matchDimension(service.GetEnv(), identity.GetEnv())
	if env == matchPriorityNone {
		return matchPriorityNone
	}
	if namespace == matchPriorityExact && env == matchPriorityExact {
		return matchPriorityExact
	}
	return matchPriorityFallback
}

func selectBestSnapshotsForIdentity(snapshots []*controlv1.ServiceSnapshot, identity *controlv1.DataplaneIdentity) []*controlv1.ServiceSnapshot {
	return newResourceArbitrator(snapshots, nil, identity).SelectedSnapshots()
}

func selectBestSnapshotMapForIdentity(snapshots []*controlv1.ServiceSnapshot, identity *controlv1.DataplaneIdentity) map[string]*controlv1.ServiceSnapshot {
	best := make(map[string]*controlv1.ServiceSnapshot)
	priorities := make(map[string]matchPriority)
	for _, snapshot := range snapshots {
		if snapshot == nil || snapshot.GetService() == nil {
			continue
		}
		priority := matchIdentityScope(snapshot.GetService(), identity)
		if priority == matchPriorityNone {
			continue
		}
		key := resourceFamilyKey(snapshot.GetService())
		if priority >= priorities[key] {
			best[key] = snapshot
			priorities[key] = priority
		}
	}
	return best
}

func selectBestRoutePoliciesForIdentity(policies []*controlv1.RoutePolicy, identity *controlv1.DataplaneIdentity) []*controlv1.RoutePolicy {
	return newResourceArbitrator(nil, policies, identity).SelectedPolicies()
}

func selectBestRoutePolicyMapForIdentity(policies []*controlv1.RoutePolicy, identity *controlv1.DataplaneIdentity) map[string]*controlv1.RoutePolicy {
	best := make(map[string]*controlv1.RoutePolicy)
	priorities := make(map[string]matchPriority)
	for _, policy := range policies {
		if policy == nil || policy.GetService() == nil {
			continue
		}
		priority := matchIdentityScope(policy.GetService(), identity)
		if priority == matchPriorityNone {
			continue
		}
		key := resourceFamilyKey(policy.GetService())
		if priority >= priorities[key] {
			best[key] = policy
			priorities[key] = priority
		}
	}
	return best
}

func resourceFamilyKey(service *controlv1.ServiceRef) string {
	if service == nil {
		return ""
	}
	return strings.TrimSpace(service.GetNamespace()) + "/" + strings.TrimSpace(service.GetService())
}

func collectSnapshots(values map[string]*controlv1.ServiceSnapshot) []*controlv1.ServiceSnapshot {
	result := make([]*controlv1.ServiceSnapshot, 0, len(values))
	for _, snapshot := range values {
		if snapshot == nil {
			continue
		}
		result = append(result, snapshot)
	}
	return result
}

func collectPolicies(values map[string]*controlv1.RoutePolicy) []*controlv1.RoutePolicy {
	result := make([]*controlv1.RoutePolicy, 0, len(values))
	for _, policy := range values {
		if policy == nil {
			continue
		}
		result = append(result, policy)
	}
	return result
}

func newResourceArbitrator(snapshots []*controlv1.ServiceSnapshot, policies []*controlv1.RoutePolicy, identity *controlv1.DataplaneIdentity) resourceArbitrator {
	return resourceArbitrator{
		snapshots: selectBestSnapshotMapForIdentity(snapshots, identity),
		policies:  selectBestRoutePolicyMapForIdentity(policies, identity),
	}
}

func newArbitrationCache(snapshots []*controlv1.ServiceSnapshot, policies []*controlv1.RoutePolicy) *arbitrationCache {
	return &arbitrationCache{
		snapshots: snapshots,
		policies:  policies,
		byKey:     make(map[string]resourceArbitrator),
	}
}

func (c *arbitrationCache) ForIdentity(identity *controlv1.DataplaneIdentity) resourceArbitrator {
	if c == nil {
		return resourceArbitrator{}
	}
	key := identityCacheKey(identity)
	if arbitrator, ok := c.byKey[key]; ok {
		return arbitrator
	}
	arbitrator := newResourceArbitrator(c.snapshots, c.policies, identity)
	c.byKey[key] = arbitrator
	return arbitrator
}

func (c *arbitrationCache) ForSubscriber(subscriber *subscriber) resourceArbitrator {
	if subscriber == nil {
		return resourceArbitrator{}
	}
	return c.ForIdentity(subscriber.identity)
}

func identityCacheKey(identity *controlv1.DataplaneIdentity) string {
	if identity == nil {
		return ""
	}
	return identity.GetNamespace() + "/" + identity.GetEnv() + "/" + identity.GetDataplaneId() + "/" + identity.GetNodeId()
}

func (a resourceArbitrator) SelectedSnapshots() []*controlv1.ServiceSnapshot {
	return collectSnapshots(a.snapshots)
}

func (a resourceArbitrator) SelectedPolicies() []*controlv1.RoutePolicy {
	return collectPolicies(a.policies)
}

func (a resourceArbitrator) SnapshotForTarget(target model.ServiceRef) *controlv1.ServiceSnapshot {
	return a.snapshots[resourceFamilyKey(&controlv1.ServiceRef{
		Service:   target.Service,
		Namespace: target.Namespace,
		Env:       target.Env,
		Port:      target.Port,
	})]
}

func (a resourceArbitrator) PolicyForTarget(target model.ServiceRef) *controlv1.RoutePolicy {
	return a.policies[resourceFamilyKey(&controlv1.ServiceRef{
		Service:   target.Service,
		Namespace: target.Namespace,
		Env:       target.Env,
		Port:      target.Port,
	})]
}

func (a resourceArbitrator) AllowsSnapshot(snapshot *controlv1.ServiceSnapshot) bool {
	if snapshot == nil || snapshot.GetService() == nil {
		return false
	}
	best, ok := a.snapshots[resourceFamilyKey(snapshot.GetService())]
	return ok && best == snapshot
}

func (a resourceArbitrator) AllowsPolicy(policy *controlv1.RoutePolicy) bool {
	if policy == nil || policy.GetService() == nil {
		return false
	}
	best, ok := a.policies[resourceFamilyKey(policy.GetService())]
	return ok && best == policy
}

func selectorFromResponse(resp *controlv1.ConnectResponse, fallbackTarget model.ServiceRef) resourceSelector {
	selector := resourceSelector{
		target:              fallbackTarget,
		requireSubscription: strings.TrimSpace(fallbackTarget.Service) != "",
	}
	if resp == nil {
		return selector
	}
	switch body := resp.GetBody().(type) {
	case *controlv1.ConnectResponse_ServiceSnapshot:
		if snapshot := body.ServiceSnapshot; snapshot != nil {
			return selectorFromSnapshot(snapshot, fallbackTarget, selector.requireSubscription)
		}
	case *controlv1.ConnectResponse_ServiceSnapshotDeleted:
		if deleted := body.ServiceSnapshotDeleted; deleted != nil {
			return selectorFromSnapshotDeleted(deleted, fallbackTarget, selector.requireSubscription)
		}
	case *controlv1.ConnectResponse_RoutePolicy:
		if policy := body.RoutePolicy; policy != nil {
			return selectorFromRoutePolicy(policy, fallbackTarget, true)
		}
	}
	return selector
}

func selectorFromSnapshotDeleted(deleted *controlv1.ServiceSnapshotDeleted, fallbackTarget model.ServiceRef, requireSubscription bool) resourceSelector {
	selector := resourceSelector{
		target:              fallbackTarget,
		requireSubscription: requireSubscription,
	}
	if deleted != nil {
		selector.service = deleted.GetService()
		if strings.TrimSpace(selector.target.Service) == "" && deleted.GetService() != nil {
			selector.target = toModelTarget(deleted.GetService())
			selector.requireSubscription = true
		}
	}
	return selector
}

func selectorFromSnapshot(snapshot *controlv1.ServiceSnapshot, fallbackTarget model.ServiceRef, requireSubscription bool) resourceSelector {
	selector := resourceSelector{
		target:              fallbackTarget,
		requireSubscription: requireSubscription,
	}
	if snapshot != nil {
		selector.service = snapshot.GetService()
		if strings.TrimSpace(selector.target.Service) == "" && snapshot.GetService() != nil {
			selector.target = toModelTarget(snapshot.GetService())
			selector.requireSubscription = true
		}
	}
	return selector
}

func selectorFromRoutePolicy(policy *controlv1.RoutePolicy, fallbackTarget model.ServiceRef, requireSubscription bool) resourceSelector {
	selector := resourceSelector{
		target:              fallbackTarget,
		requireSubscription: requireSubscription,
		requireIdentity:     true,
	}
	if policy != nil {
		selector.service = policy.GetService()
		if strings.TrimSpace(selector.target.Service) == "" && policy.GetService() != nil {
			selector.target = toModelTarget(policy.GetService())
			selector.requireSubscription = true
		}
	}
	return selector
}

func selectorFromSubscriber(subscriber *subscriber) subscriberSelector {
	if subscriber == nil {
		return subscriberSelector{}
	}
	return subscriberSelector{
		identity: subscriber.identity,
		targets:  subscriber.targets,
	}
}

func matchesSelectors(subscriber subscriberSelector, resource resourceSelector) bool {
	return evaluateSelectorMatch(subscriber, resource).matched()
}

func evaluateSelectorMatch(subscriber subscriberSelector, resource resourceSelector) selectorMatch {
	result := selectorMatch{
		subscription: matchPriorityExact,
		identity:     matchPriorityExact,
	}
	if resource.requireSubscription {
		result.subscription = subscriber.matchTarget(resource.target)
	}
	if resource.requireIdentity {
		result.identity = matchIdentityScope(resource.service, subscriber.identity)
	}
	return result
}

func (m selectorMatch) matched() bool {
	return m.subscription != matchPriorityNone && m.identity != matchPriorityNone
}

func toModelTarget(service *controlv1.ServiceRef) model.ServiceRef {
	if service == nil {
		return model.ServiceRef{}
	}
	return model.ServiceRef{
		Service:   service.GetService(),
		Namespace: service.GetNamespace(),
		Env:       service.GetEnv(),
		Port:      service.GetPort(),
	}
}

func (s *subscriber) shouldReceive(target model.ServiceRef) bool {
	return selectorFromSubscriber(s).acceptsTarget(target)
}

func (s subscriberSelector) acceptsTarget(target model.ServiceRef) bool {
	return s.matchTarget(target) != matchPriorityNone
}

func (s subscriberSelector) matchTarget(target model.ServiceRef) matchPriority {
	if strings.TrimSpace(target.Service) == "" {
		return matchPriorityFallback
	}
	if len(s.targets) == 0 {
		return matchPriorityFallback
	}
	if _, ok := s.targets[targetKey(target)]; ok {
		return matchPriorityExact
	}
	for _, subscribed := range s.targets {
		if matchTargetFamily(subscribed, target) == matchPriorityFallback {
			return matchPriorityFallback
		}
	}
	return matchPriorityNone
}

func matchTargetFamily(subscribed, resource model.ServiceRef) matchPriority {
	if strings.TrimSpace(subscribed.Service) == "" || strings.TrimSpace(resource.Service) == "" {
		return matchPriorityNone
	}
	if strings.TrimSpace(subscribed.Namespace) != strings.TrimSpace(resource.Namespace) {
		return matchPriorityNone
	}
	if strings.TrimSpace(subscribed.Service) != strings.TrimSpace(resource.Service) {
		return matchPriorityNone
	}
	if strings.TrimSpace(subscribed.Env) == "" || strings.TrimSpace(resource.Env) == "" {
		return matchPriorityFallback
	}
	if strings.TrimSpace(subscribed.Env) == strings.TrimSpace(resource.Env) {
		return matchPriorityExact
	}
	return matchPriorityNone
}
