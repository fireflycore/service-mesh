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
	case *controlv1.ConnectResponse_RoutePolicy:
		if policy := body.RoutePolicy; policy != nil {
			return selectorFromRoutePolicy(policy, fallbackTarget, true)
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
	return matchPriorityNone
}
