package client

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type State struct {
	mu           sync.RWMutex
	snapshots    map[string]*controlv1.ServiceSnapshot
	routePolicy  map[string]*controlv1.RoutePolicy
	lastSnapshot *controlv1.ServiceSnapshot
	lastPolicy   *controlv1.RoutePolicy
}

func (s *State) SetSnapshot(snapshot *controlv1.ServiceSnapshot) {
	if snapshot == nil || snapshot.GetService() == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshots == nil {
		s.snapshots = make(map[string]*controlv1.ServiceSnapshot)
	}
	s.snapshots[serviceKey(snapshot.GetService().GetNamespace(), snapshot.GetService().GetEnv(), snapshot.GetService().GetService())] = snapshot
	s.lastSnapshot = snapshot
}

func (s *State) SetRoutePolicy(policy *controlv1.RoutePolicy) {
	if policy == nil || policy.GetService() == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routePolicy == nil {
		s.routePolicy = make(map[string]*controlv1.RoutePolicy)
	}
	s.routePolicy[serviceKey(policy.GetService().GetNamespace(), policy.GetService().GetEnv(), policy.GetService().GetService())] = policy
	s.lastPolicy = policy
}

func (s *State) Snapshot() *controlv1.ServiceSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSnapshot
}

func (s *State) RoutePolicy() *controlv1.RoutePolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastPolicy
}

func (s *State) ResolveSnapshot(target model.ServiceRef) (model.ServiceSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := s.lookupSnapshot(target)
	if snapshot == nil {
		return model.ServiceSnapshot{}, false
	}

	return model.ServiceSnapshot{
		Service: model.ServiceRef{
			Service:   snapshot.GetService().GetService(),
			Namespace: snapshot.GetService().GetNamespace(),
			Env:       snapshot.GetService().GetEnv(),
			Port:      snapshot.GetService().GetPort(),
		},
		Endpoints: toModelEndpoints(snapshot.GetEndpoints()),
		Revision:  snapshot.GetRevision(),
	}, true
}

func (s *State) ResolveRoutePolicy(target model.ServiceRef) (*controlv1.RoutePolicy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	policy := s.lookupPolicy(target)
	return policy, policy != nil
}

type Client struct {
	cfg   config.ControlPlaneConfig
	state *State
}

func New(cfg config.ControlPlaneConfig) *Client {
	return &Client{
		cfg:   cfg,
		state: &State{},
	}
}

func (c *Client) State() *State {
	return c.state
}

func (c *Client) Run(ctx context.Context, identity *controlv1.DataplaneIdentity) error {
	if identity == nil {
		return nil
	}

	backoff := heartbeatInterval(c.cfg)
	for {
		err := c.connectOnce(ctx, identity)
		if err == nil || ctx.Err() != nil {
			return err
		}

		slog.Warn("controlplane stream disconnected",
			slog.String("target", c.cfg.Target),
			slog.String("error", err.Error()),
		)

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) connectOnce(ctx context.Context, identity *controlv1.DataplaneIdentity) error {
	dialCtx, cancel := context.WithTimeout(ctx, connectTimeout(c.cfg))
	defer cancel()

	conn, err := grpc.DialContext(
		dialCtx,
		c.cfg.Target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(ctx)
	if err != nil {
		return err
	}

	if err := stream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Register{
			Register: &controlv1.DataplaneRegister{
				Identity: identity,
				Version:  "v1",
			},
		},
	}); err != nil {
		return err
	}

	recvErrCh := make(chan error, 1)
	go func() {
		recvErrCh <- c.recvLoop(stream)
	}()

	ticker := time.NewTicker(heartbeatInterval(c.cfg))
	defer ticker.Stop()

	for {
		select {
		case err := <-recvErrCh:
			if err == io.EOF {
				return nil
			}
			return err
		case <-ticker.C:
			if err := stream.Send(&controlv1.ConnectRequest{
				Body: &controlv1.ConnectRequest_Heartbeat{
					Heartbeat: &controlv1.DataplaneHeartbeat{
						DataplaneId:     identity.GetDataplaneId(),
						TimestampUnixMs: time.Now().UnixMilli(),
					},
				},
			}); err != nil {
				return err
			}
		case <-ctx.Done():
			_ = stream.CloseSend()
			return ctx.Err()
		}
	}
}

func (c *Client) recvLoop(stream grpc.BidiStreamingClient[controlv1.ConnectRequest, controlv1.ConnectResponse]) error {
	for {
		resp, err := stream.Recv()
		if err != nil {
			return err
		}

		switch body := resp.GetBody().(type) {
		case *controlv1.ConnectResponse_ServiceSnapshot:
			c.state.SetSnapshot(body.ServiceSnapshot)
		case *controlv1.ConnectResponse_RoutePolicy:
			c.state.SetRoutePolicy(body.RoutePolicy)
		}
	}
}

func heartbeatInterval(cfg config.ControlPlaneConfig) time.Duration {
	if cfg.HeartbeatIntervalMS == 0 {
		return 3 * time.Second
	}
	return time.Duration(cfg.HeartbeatIntervalMS) * time.Millisecond
}

func connectTimeout(cfg config.ControlPlaneConfig) time.Duration {
	if cfg.ConnectTimeoutMS == 0 {
		return time.Second
	}
	return time.Duration(cfg.ConnectTimeoutMS) * time.Millisecond
}

func (s *State) lookupSnapshot(target model.ServiceRef) *controlv1.ServiceSnapshot {
	if s.snapshots == nil {
		return nil
	}
	if snapshot, ok := s.snapshots[serviceKey(target.Namespace, target.Env, target.Service)]; ok {
		return snapshot
	}
	return s.snapshots[serviceKey(target.Namespace, "", target.Service)]
}

func (s *State) lookupPolicy(target model.ServiceRef) *controlv1.RoutePolicy {
	if s.routePolicy == nil {
		return nil
	}
	if policy, ok := s.routePolicy[serviceKey(target.Namespace, target.Env, target.Service)]; ok {
		return policy
	}
	return s.routePolicy[serviceKey(target.Namespace, "", target.Service)]
}

func serviceKey(namespace, env, service string) string {
	namespace = strings.TrimSpace(namespace)
	env = strings.TrimSpace(env)
	service = strings.TrimSpace(service)
	if env == "" {
		return namespace + "/" + service
	}
	return namespace + "/" + env + "/" + service
}

func toModelEndpoints(endpoints []*controlv1.Endpoint) []model.Endpoint {
	result := make([]model.Endpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint == nil {
			continue
		}
		result = append(result, model.Endpoint{
			Address: endpoint.GetAddress(),
			Port:    endpoint.GetPort(),
			Weight:  endpoint.GetWeight(),
		})
	}
	return result
}
