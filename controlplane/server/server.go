package server

import (
	"io"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"google.golang.org/grpc"
)

// Server 是控制面的最小 gRPC 服务实现。
type Server struct {
	controlv1.UnimplementedMeshControlPlaneServiceServer

	store *snapshot.Store
}

// New 用给定的 snapshot store 创建控制面服务。
func New(store *snapshot.Store) *Server {
	return &Server{
		store: store,
	}
}

// Connect 维护一条双向流，按消息类型分发到 register / heartbeat 处理器。
func (s *Server) Connect(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse]) error {
	for {
		// 服务端持续从同一条流上读取 register/heartbeat 等消息。
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				// 客户端正常结束流时，服务端也直接结束本次连接生命周期。
				return nil
			}
			return err
		}

		switch body := req.GetBody().(type) {
		case *controlv1.ConnectRequest_Register:
			// register 触发一次“回放当前状态”。
			if err := s.handleRegister(stream, body.Register); err != nil {
				return err
			}
		case *controlv1.ConnectRequest_Heartbeat:
			// heartbeat 当前只用于保活，后续可以在这里扩展 ACK 或状态回写。
			if err := s.handleHeartbeat(stream, body.Heartbeat); err != nil {
				return err
			}
		}
	}
}

// handleRegister 在 dataplane 首次注册时回放当前快照与策略。
func (s *Server) handleRegister(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse], register *controlv1.DataplaneRegister) error {
	if register == nil || register.GetIdentity() == nil {
		return nil
	}

	service := &controlv1.ServiceRef{
		// 这里把 dataplane identity 收敛成 service 维度键，后续统一用来查快照/策略。
		Service:   register.GetIdentity().GetService(),
		Namespace: register.GetIdentity().GetNamespace(),
		Env:       register.GetIdentity().GetEnv(),
	}

	serviceSnapshot, routePolicy := s.store.Lookup(service)
	if serviceSnapshot != nil {
		// 先发服务快照，让 dataplane 尽快拿到可路由实例列表。
		if err := stream.Send(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_ServiceSnapshot{
				ServiceSnapshot: serviceSnapshot,
			},
		}); err != nil {
			return err
		}
	}
	if routePolicy != nil {
		// 再发路由策略，让 dataplane 能覆盖 timeout/retry 等行为。
		if err := stream.Send(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_RoutePolicy{
				RoutePolicy: routePolicy,
			},
		}); err != nil {
			return err
		}
	}

	return nil
}

// handleHeartbeat 为后续更复杂的控制面状态机保留入口。
func (s *Server) handleHeartbeat(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse], heartbeat *controlv1.DataplaneHeartbeat) error {
	if heartbeat == nil || heartbeat.GetDataplaneId() == "" {
		// 当前 heartbeat 不严格报错，尽量保持控制面最小实现简单可用。
		return nil
	}

	return nil
}
