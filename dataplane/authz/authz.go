package authz

import (
	"context"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
)

type Authorizer interface {
	Check(ctx context.Context, req *invokev1.UnaryInvokeRequest) error
}

type AllowAll struct{}

func NewAllowAll() *AllowAll {
	return &AllowAll{}
}

func (a *AllowAll) Check(_ context.Context, _ *invokev1.UnaryInvokeRequest) error {
	return nil
}
