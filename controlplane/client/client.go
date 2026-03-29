package client

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/config"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type State struct {
	mu           sync.RWMutex
	lastSnapshot *controlv1.ServiceSnapshot
	lastPolicy   *controlv1.RoutePolicy
}

func (s *State) SetSnapshot(snapshot *controlv1.ServiceSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSnapshot = snapshot
}

func (s *State) SetRoutePolicy(policy *controlv1.RoutePolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
