package snapshot

import (
	"fmt"
	"strings"
	"sync"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
)

// Store 保存控制面当前已知的基础快照与路由策略。
type Store struct {
	mu sync.RWMutex
	// snapshots 保存服务发现结果，routePolicies 保存调用策略。
	snapshots     map[string]*controlv1.ServiceSnapshot
	routePolicies map[string]*controlv1.RoutePolicy
	// snapshotVersions 用于给没有外部 revision 的 source 快照生成稳定递增版本。
	snapshotVersions map[string]uint64
}

// NewStore 创建一个空的内存快照仓。
func NewStore() *Store {
	return &Store{
		snapshots:        make(map[string]*controlv1.ServiceSnapshot),
		routePolicies:    make(map[string]*controlv1.RoutePolicy),
		snapshotVersions: make(map[string]uint64),
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

// DeleteServiceSnapshot 按目标服务删除已有快照。
func (s *Store) DeleteServiceSnapshot(target model.ServiceRef) bool {
	if strings.TrimSpace(target.Service) == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := serviceKey(&controlv1.ServiceRef{
		Service:   target.Service,
		Namespace: target.Namespace,
		Env:       target.Env,
		Port:      target.Port,
	})
	if _, ok := s.snapshots[key]; !ok {
		return false
	}
	delete(s.snapshots, key)
	return true
}

// PutModelSnapshot 把 source 层内部快照转换为控制面 proto 快照并写入 store。
func (s *Store) PutModelSnapshot(snapshot model.ServiceSnapshot) (*controlv1.ServiceSnapshot, bool) {
	if strings.TrimSpace(snapshot.Service.Service) == "" {
		return nil, false
	}

	protoSnapshot := toProtoSnapshot(snapshot)
	key := serviceKey(protoSnapshot.GetService())

	s.mu.Lock()
	defer s.mu.Unlock()

	if current, ok := s.snapshots[key]; ok {
		if protoSnapshot.GetRevision() != "" {
			if equalSnapshots(current, protoSnapshot) {
				return current, false
			}
		} else if equalSnapshotsIgnoringRevision(current, protoSnapshot) {
			return current, false
		}
	}

	if protoSnapshot.GetRevision() == "" {
		s.snapshotVersions[key]++
		protoSnapshot.Revision = fmt.Sprintf("source-%d", s.snapshotVersions[key])
	}

	s.snapshots[key] = protoSnapshot
	return protoSnapshot, true
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

// AllServiceSnapshots 返回当前 store 中所有服务快照的副本切片。
func (s *Store) AllServiceSnapshots() []*controlv1.ServiceSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*controlv1.ServiceSnapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		if snapshot == nil {
			continue
		}
		result = append(result, snapshot)
	}
	return result
}

// AllRoutePolicies 返回当前 store 中所有路由策略的副本切片。
func (s *Store) AllRoutePolicies() []*controlv1.RoutePolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*controlv1.RoutePolicy, 0, len(s.routePolicies))
	for _, policy := range s.routePolicies {
		if policy == nil {
			continue
		}
		result = append(result, policy)
	}
	return result
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

func toProtoSnapshot(snapshot model.ServiceSnapshot) *controlv1.ServiceSnapshot {
	endpoints := make([]*controlv1.Endpoint, 0, len(snapshot.Endpoints))
	for _, endpoint := range snapshot.Endpoints {
		endpoints = append(endpoints, &controlv1.Endpoint{
			Address: endpoint.Address,
			Port:    endpoint.Port,
			Weight:  endpoint.Weight,
		})
	}

	return &controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   snapshot.Service.Service,
			Namespace: snapshot.Service.Namespace,
			Env:       snapshot.Service.Env,
			Port:      snapshot.Service.Port,
		},
		Endpoints: endpoints,
		Revision:  snapshot.Revision,
	}
}

func equalSnapshots(a, b *controlv1.ServiceSnapshot) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !equalSnapshotsIgnoringRevision(a, b) {
		return false
	}
	return a.GetRevision() == b.GetRevision()
}

func equalSnapshotsIgnoringRevision(a, b *controlv1.ServiceSnapshot) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.GetService().GetService() != b.GetService().GetService() ||
		a.GetService().GetNamespace() != b.GetService().GetNamespace() ||
		a.GetService().GetEnv() != b.GetService().GetEnv() ||
		a.GetService().GetPort() != b.GetService().GetPort() {
		return false
	}
	if len(a.GetEndpoints()) != len(b.GetEndpoints()) {
		return false
	}
	for i := range a.GetEndpoints() {
		left := a.GetEndpoints()[i]
		right := b.GetEndpoints()[i]
		if left.GetAddress() != right.GetAddress() ||
			left.GetPort() != right.GetPort() ||
			left.GetWeight() != right.GetWeight() {
			return false
		}
	}
	return true
}
