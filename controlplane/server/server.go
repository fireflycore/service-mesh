package server

import (
	"io"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"google.golang.org/grpc"
)

type Server struct {
	controlv1.UnimplementedMeshControlPlaneServiceServer

	store *snapshot.Store
}

func New(store *snapshot.Store) *Server {
	return &Server{
		store: store,
	}
}

func (s *Server) Connect(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse]) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch body := req.GetBody().(type) {
		case *controlv1.ConnectRequest_Register:
			if err := s.handleRegister(stream, body.Register); err != nil {
				return err
			}
		case *controlv1.ConnectRequest_Heartbeat:
			if err := s.handleHeartbeat(stream, body.Heartbeat); err != nil {
				return err
			}
		}
	}
}

func (s *Server) handleRegister(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse], register *controlv1.DataplaneRegister) error {
	if register == nil || register.GetIdentity() == nil {
		return nil
	}

	service := &controlv1.ServiceRef{
		Service:   register.GetIdentity().GetService(),
		Namespace: register.GetIdentity().GetNamespace(),
		Env:       register.GetIdentity().GetEnv(),
	}

	serviceSnapshot, routePolicy := s.store.Lookup(service)
	if serviceSnapshot != nil {
		if err := stream.Send(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_ServiceSnapshot{
				ServiceSnapshot: serviceSnapshot,
			},
		}); err != nil {
			return err
		}
	}
	if routePolicy != nil {
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

func (s *Server) handleHeartbeat(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse], heartbeat *controlv1.DataplaneHeartbeat) error {
	if heartbeat == nil || heartbeat.GetDataplaneId() == "" {
		return nil
	}

	return nil
}
