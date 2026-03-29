package snapshot

import (
	"strings"
	"sync"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
)

// Store 保存控制面当前已知的基础快照与路由策略。
type Store struct {
	mu sync.RWMutex
	// snapshots 保存服务发现结果，routePolicies 保存调用策略。
	snapshots     map[string]*controlv1.ServiceSnapshot
	routePolicies map[string]*controlv1.RoutePolicy
}

// NewStore 创建一个空的内存快照仓。
func NewStore() *Store {
	return &Store{
		snapshots:     make(map[string]*controlv1.ServiceSnapshot),
		routePolicies: make(map[string]*controlv1.RoutePolicy),
	}
}

// PutServiceSnapshot 以 serviceKey 为键写入服务快照。
func (s *Store) PutServiceSnapshot(snapshot *controlv1.ServiceSnapshot) {
	if snapshot == nil || snapshot.GetService() == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// 新值直接覆盖旧值，保持“最后一次写入生效”。
	s.snapshots[serviceKey(snapshot.GetService())] = snapshot
}

// PutRoutePolicy 以 serviceKey 为键写入路由策略。
func (s *Store) PutRoutePolicy(policy *controlv1.RoutePolicy) {
	if policy == nil || policy.GetService() == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.routePolicies[serviceKey(policy.GetService())] = policy
}

// Lookup 按服务身份读取快照与策略。
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
		// 只要快照或策略命中任意一个，就直接返回当前结果。
		return snapshot, policy
	}

	// 这里保留不带 env 的回退路径，
	// 兼容只按 namespace/service 下发的旧快照或策略。
	fallbackKey := service.GetNamespace() + "/" + service.GetService()
	return s.snapshots[fallbackKey], s.routePolicies[fallbackKey]
}

// serviceKey 把 namespace / env / service 收敛成统一索引键。
func serviceKey(service *controlv1.ServiceRef) string {
	namespace := strings.TrimSpace(service.GetNamespace())
	env := strings.TrimSpace(service.GetEnv())
	name := strings.TrimSpace(service.GetService())
	if env == "" {
		return namespace + "/" + name
	}
	return namespace + "/" + env + "/" + name
}
