package snapshot

import (
	"strings"
	"sync"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
)

type Store struct {
	mu            sync.RWMutex
	snapshots     map[string]*controlv1.ServiceSnapshot
	routePolicies map[string]*controlv1.RoutePolicy
}

func NewStore() *Store {
	return &Store{
		snapshots:     make(map[string]*controlv1.ServiceSnapshot),
		routePolicies: make(map[string]*controlv1.RoutePolicy),
	}
}

func (s *Store) PutServiceSnapshot(snapshot *controlv1.ServiceSnapshot) {
	if snapshot == nil || snapshot.GetService() == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[serviceKey(snapshot.GetService())] = snapshot
}

func (s *Store) PutRoutePolicy(policy *controlv1.RoutePolicy) {
	if policy == nil || policy.GetService() == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.routePolicies[serviceKey(policy.GetService())] = policy
}

func (s *Store) Lookup(service *controlv1.ServiceRef) (*controlv1.ServiceSnapshot, *controlv1.RoutePolicy) {
	if service == nil {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := serviceKey(service)
	snapshot := s.snapshots[key]
	policy := s.routePolicies[key]
	if snapshot != nil || policy != nil {
		return snapshot, policy
	}

	// 第八版 register identity 里还没有 env 字段，
	// 因此服务端会回退到“忽略 env”的默认快照键。
	fallbackKey := service.GetNamespace() + "/" + service.GetService()
	return s.snapshots[fallbackKey], s.routePolicies[fallbackKey]
}

func serviceKey(service *controlv1.ServiceRef) string {
	namespace := strings.TrimSpace(service.GetNamespace())
	env := strings.TrimSpace(service.GetEnv())
	name := strings.TrimSpace(service.GetService())
	if env == "" {
		return namespace + "/" + name
	}
	return namespace + "/" + env + "/" + name
}
