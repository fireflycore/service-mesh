package invoke

import (
	"context"
	"errors"
	"strings"

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

// Service 是本地 MeshInvokeService 的最小实现。
//
// 它负责把一条外部传入的 Invoke 请求拆成四步：
// 1. 校验输入
// 2. 调用 authz
// 3. 解析目标 endpoint
// 4. 通过 transport 转发到真正的下游 gRPC 服务
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

// UnaryInvoke 是当前阶段最核心的本地调用入口。
//
// 当前第三版的一个重要约束是：
//   - req.Method 必须是完整的 gRPC method path
//     例如 `/acme.orders.v1.OrderService/GetOrder`
//
// 这样 transport 才能在不知道具体业务 proto Go 类型的情况下，
// 直接按原始 protobuf bytes 做通用转发。
func (s *Service) UnaryInvoke(ctx context.Context, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	if req.GetTarget() == nil || req.GetTarget().GetService() == "" {
		return nil, status.Error(codes.InvalidArgument, "target.service is required")
	}
	if req.GetMethod() == "" {
		return nil, status.Error(codes.InvalidArgument, "method is required")
	}
	if !strings.HasPrefix(req.GetMethod(), "/") {
		return nil, status.Error(codes.InvalidArgument, "method must be a full grpc method path")
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
