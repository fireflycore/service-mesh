package invoke

import (
	"context"
	"errors"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/dataplane/authz"
	"github.com/fireflycore/service-mesh/dataplane/transport"
	"github.com/fireflycore/service-mesh/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Resolver interface {
	Resolve(ctx context.Context, target model.ServiceRef) (model.Endpoint, error)
}

type Service struct {
	invokev1.UnimplementedMeshInvokeServiceServer

	authorizer authz.Authorizer
	resolver   Resolver
	transport  transport.Invoker
}

func NewService(authorizer authz.Authorizer, resolver Resolver, transport transport.Invoker) *Service {
	return &Service{
		authorizer: authorizer,
		resolver:   resolver,
		transport:  transport,
	}
}

func (s *Service) UnaryInvoke(ctx context.Context, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	if req.GetTarget() == nil || req.GetTarget().GetService() == "" {
		return nil, status.Error(codes.InvalidArgument, "target.service is required")
	}
	if req.GetMethod() == "" {
		return nil, status.Error(codes.InvalidArgument, "method is required")
	}

	if err := s.authorizer.Check(ctx, req); err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "authz check failed: %v", err)
	}

	target := model.ServiceRef{
		Service:   req.GetTarget().GetService(),
		Namespace: req.GetTarget().GetNamespace(),
		Env:       req.GetTarget().GetEnv(),
		Port:      req.GetTarget().GetPort(),
	}

	endpoint, err := s.resolver.Resolve(ctx, target)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, err.Error())
		}
		return nil, status.Errorf(codes.Unavailable, "resolve target failed: %v", err)
	}

	resp, err := s.transport.Invoke(ctx, endpoint, req)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "invoke target failed: %v", err)
	}

	return resp, nil
}
