package balancer

import (
	"errors"
	"sync"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// Picker 表示一个实例选择策略。
type Picker interface {
	Pick(snapshot model.ServiceSnapshot) (model.Endpoint, error)
}

// RoundRobin 是当前阶段最简单、最稳定的 endpoint 选择策略。
//
// 它按 service 维度维护游标，确保同一个服务的多次请求能够轮询不同实例。
type RoundRobin struct {
	mu      sync.Mutex
	current map[string]uint64
}

// NewRoundRobin 创建轮询 balancer。
func NewRoundRobin() *RoundRobin {
	return &RoundRobin{
		current: make(map[string]uint64),
	}
}

// Pick 从快照中挑选一个 endpoint。
func (b *RoundRobin) Pick(snapshot model.ServiceSnapshot) (model.Endpoint, error) {
	if len(snapshot.Endpoints) == 0 {
		return model.Endpoint{}, errors.New("no endpoints available")
	}

	key := snapshot.Service.Namespace + "/" + snapshot.Service.Env + "/" + snapshot.Service.Service

	b.mu.Lock()
	defer b.mu.Unlock()

	index := b.current[key] % uint64(len(snapshot.Endpoints))
	b.current[key]++

	return snapshot.Endpoints[index], nil
}
