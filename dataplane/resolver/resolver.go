package resolver

import (
	"context"

	"github.com/fireflycore/service-mesh/dataplane/balancer"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source"
)

type Resolver struct {
	source   source.Provider
	balancer balancer.Picker
}

func New(provider source.Provider, picker balancer.Picker) *Resolver {
	return &Resolver{
		source:   provider,
		balancer: picker,
	}
}

func (r *Resolver) Resolve(ctx context.Context, target model.ServiceRef) (model.Endpoint, error) {
	snapshot, err := r.source.Resolve(ctx, target)
	if err != nil {
		return model.Endpoint{}, err
	}

	return r.balancer.Pick(snapshot)
}
