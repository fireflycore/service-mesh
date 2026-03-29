package balancer

import (
	"errors"
	"sync"

	"github.com/fireflycore/service-mesh/pkg/model"
)

type Picker interface {
	Pick(snapshot model.ServiceSnapshot) (model.Endpoint, error)
}

type RoundRobin struct {
	mu      sync.Mutex
	current map[string]uint64
}

func NewRoundRobin() *RoundRobin {
	return &RoundRobin{
		current: make(map[string]uint64),
	}
}

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
