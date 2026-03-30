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

func matchesDataplaneIdentity(service *controlv1.ServiceRef, identity *controlv1.DataplaneIdentity) bool {
	return matchesIdentityScope(service, identity)
}

func matchesIdentityScope(service *controlv1.ServiceRef, identity *controlv1.DataplaneIdentity) bool {
	if service == nil || identity == nil {
		return true
	}
	if !matchesDimension(service.GetNamespace(), identity.GetNamespace()) {
		return false
	}
	if !matchesDimension(service.GetEnv(), identity.GetEnv()) {
		return false
	}
	return true
}

func matchesDimension(serviceValue, identityValue string) bool {
	serviceValue = strings.TrimSpace(serviceValue)
	identityValue = strings.TrimSpace(identityValue)
	return serviceValue == "" || identityValue == "" || serviceValue == identityValue
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
	if resource.requireSubscription && !subscriber.acceptsTarget(resource.target) {
		return false
	}
	if resource.requireIdentity && !matchesIdentityScope(resource.service, subscriber.identity) {
		return false
	}
	return true
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
	if strings.TrimSpace(target.Service) == "" {
		return true
	}
	if len(s.targets) == 0 {
		return true
	}
	_, ok := s.targets[targetKey(target)]
	return ok
}
