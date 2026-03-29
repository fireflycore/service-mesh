package transport

import (
	"context"
	"fmt"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
)

type Invoker interface {
	Invoke(ctx context.Context, endpoint model.Endpoint, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error)
}

type NotImplemented struct{}

func NewNotImplemented() *NotImplemented {
	return &NotImplemented{}
}

func (t *NotImplemented) Invoke(_ context.Context, endpoint model.Endpoint, _ *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	return nil, fmt.Errorf("transport invoke is not implemented for %s:%d", endpoint.Address, endpoint.Port)
}
