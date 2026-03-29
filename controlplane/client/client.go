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

// State 保存 controlplane 最近一次下发到 dataplane 的可生效状态。
type State struct {
	mu sync.RWMutex
	// snapshots/routePolicy 按 serviceKey 保存“可按服务查询”的当前状态。
	snapshots   map[string]*controlv1.ServiceSnapshot
	routePolicy map[string]*controlv1.RoutePolicy
	// lastSnapshot/lastPolicy 主要服务于测试与调试观察。
	lastSnapshot *controlv1.ServiceSnapshot
	lastPolicy   *controlv1.RoutePolicy
}

// SetSnapshot 写入并覆盖指定服务的最新快照。
func (s *State) SetSnapshot(snapshot *controlv1.ServiceSnapshot) {
	if snapshot == nil || snapshot.GetService() == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshots == nil {
		// 懒初始化可以让空 State 在没有控制面消息时保持零值可用。
		s.snapshots = make(map[string]*controlv1.ServiceSnapshot)
	}
	s.snapshots[serviceKey(snapshot.GetService().GetNamespace(), snapshot.GetService().GetEnv(), snapshot.GetService().GetService())] = snapshot
	s.lastSnapshot = snapshot
}

// SetRoutePolicy 写入并覆盖指定服务的最新策略。
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

// Snapshot 返回最近一次收到的快照，主要给测试和调试使用。
func (s *State) Snapshot() *controlv1.ServiceSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSnapshot
}

// RoutePolicy 返回最近一次收到的策略，主要给测试和调试使用。
func (s *State) RoutePolicy() *controlv1.RoutePolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastPolicy
}

// ResolveSnapshot 把控制面快照转换为 dataplane 内部统一模型。
func (s *State) ResolveSnapshot(target model.ServiceRef) (model.ServiceSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// lookupSnapshot 内部会做“精确 env 命中 -> 忽略 env 回退”的两级查找。
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
		// endpoint 列表需要从 proto 模型转换为 dataplane 内部模型。
		Endpoints: toModelEndpoints(snapshot.GetEndpoints()),
		Revision:  snapshot.GetRevision(),
	}, true
}

// ResolveRoutePolicy 根据目标服务解析控制面下发的策略。
func (s *State) ResolveRoutePolicy(target model.ServiceRef) (*controlv1.RoutePolicy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	policy := s.lookupPolicy(target)
	return policy, policy != nil
}

// Client 负责维持 dataplane 到 controlplane 的长连接。
type Client struct {
	// cfg 决定目标地址、心跳频率和建链超时。
	cfg config.ControlPlaneConfig
	// state 暴露给 dataplane 其他组件读取控制面下发结果。
	state *State
}

// New 创建 controlplane client。
func New(cfg config.ControlPlaneConfig) *Client {
	return &Client{
		cfg: cfg,
		// State 默认从空仓开始，等 register 成功后逐步填充。
		state: &State{},
	}
}

// State 暴露当前已缓存的控制面状态。
func (c *Client) State() *State {
	return c.state
}

// Run 在后台持续维护连接，并在断开后按固定间隔重连。
func (c *Client) Run(ctx context.Context, identity *controlv1.DataplaneIdentity) error {
	if identity == nil {
		// 没有 identity 时控制面连接没有语义，因此直接返回。
		return nil
	}

	backoff := heartbeatInterval(c.cfg)
	for {
		// 每一轮循环都代表“一次完整连接生命周期”。
		err := c.connectOnce(ctx, identity)
		if err == nil || ctx.Err() != nil {
			return err
		}

		slog.Warn("controlplane stream disconnected",
			slog.String("target", c.cfg.Target),
			slog.String("error", err.Error()),
		)

		// 当前实现用固定 backoff；后续若需要再升级为指数退避。
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// 退避结束后进入下一轮 connectOnce。
		}
	}
}

// connectOnce 建立一轮新的控制面连接并维持心跳。
func (c *Client) connectOnce(ctx context.Context, identity *controlv1.DataplaneIdentity) error {
	// 建链超时和整条 stream 生命周期区分开，避免拨号无限阻塞。
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

	// Connect 建立的是控制面双向流，而不是普通 unary 调用。
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
		// register 失败说明本轮连接尚未进入稳定阶段，直接让上层重连。
		return err
	}

	// 接收循环单独放 goroutine，主循环才能同时处理心跳和 ctx 取消。
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
				// 对端正常关闭流，视为本轮连接自然结束。
				return nil
			}
			return err
		case <-ticker.C:
			// 当前 heartbeat 只负责维持活性，不承载额外状态。
			if err := stream.Send(&controlv1.ConnectRequest{
				Body: &controlv1.ConnectRequest_Heartbeat{
					Heartbeat: &controlv1.DataplaneHeartbeat{
						DataplaneId:     identity.GetDataplaneId(),
						TimestampUnixMs: time.Now().UnixMilli(),
					},
				},
			}); err != nil {
				// 心跳发送失败通常意味着流已经失效，交给外层重连。
				return err
			}
		case <-ctx.Done():
			// CloseSend 用于提示服务端本端不再发送更多消息。
			_ = stream.CloseSend()
			return ctx.Err()
		}
	}
}

// recvLoop 持续消费控制面下发消息并写入本地 State。
func (c *Client) recvLoop(stream grpc.BidiStreamingClient[controlv1.ConnectRequest, controlv1.ConnectResponse]) error {
	for {
		resp, err := stream.Recv()
		if err != nil {
			return err
		}

		switch body := resp.GetBody().(type) {
		case *controlv1.ConnectResponse_ServiceSnapshot:
			// 服务快照主要影响 resolver/source overlay。
			c.state.SetSnapshot(body.ServiceSnapshot)
		case *controlv1.ConnectResponse_RoutePolicy:
			// 路由策略主要影响 invoke timeout/retry。
			c.state.SetRoutePolicy(body.RoutePolicy)
		}
	}
}

// heartbeatInterval 统一收敛心跳周期默认值。
func heartbeatInterval(cfg config.ControlPlaneConfig) time.Duration {
	if cfg.HeartbeatIntervalMS == 0 {
		// 与配置默认值保持一致，避免直接构造 Client 时语义漂移。
		return 3 * time.Second
	}
	return time.Duration(cfg.HeartbeatIntervalMS) * time.Millisecond
}

// connectTimeout 统一收敛控制面连接超时默认值。
func connectTimeout(cfg config.ControlPlaneConfig) time.Duration {
	if cfg.ConnectTimeoutMS == 0 {
		// 兜底 1 秒，避免拨号阶段长时间挂死。
		return time.Second
	}
	return time.Duration(cfg.ConnectTimeoutMS) * time.Millisecond
}

// lookupSnapshot 先按精确 serviceKey 查，再回退到不带 env 的键。
func (s *State) lookupSnapshot(target model.ServiceRef) *controlv1.ServiceSnapshot {
	if s.snapshots == nil {
		return nil
	}
	if snapshot, ok := s.snapshots[serviceKey(target.Namespace, target.Env, target.Service)]; ok {
		return snapshot
	}
	// 回退到不带 env 的键，兼容更粗粒度的服务快照下发。
	return s.snapshots[serviceKey(target.Namespace, "", target.Service)]
}

// lookupPolicy 先按精确 serviceKey 查，再回退到不带 env 的键。
func (s *State) lookupPolicy(target model.ServiceRef) *controlv1.RoutePolicy {
	if s.routePolicy == nil {
		return nil
	}
	if policy, ok := s.routePolicy[serviceKey(target.Namespace, target.Env, target.Service)]; ok {
		return policy
	}
	// 策略也保留相同回退路径，减少控制面演进时的兼容成本。
	return s.routePolicy[serviceKey(target.Namespace, "", target.Service)]
}

// serviceKey 统一控制面状态映射使用的字符串键格式。
func serviceKey(namespace, env, service string) string {
	namespace = strings.TrimSpace(namespace)
	env = strings.TrimSpace(env)
	service = strings.TrimSpace(service)
	if env == "" {
		return namespace + "/" + service
	}
	return namespace + "/" + env + "/" + service
}

// toModelEndpoints 把 proto endpoint 转为内部 model.Endpoint。
func toModelEndpoints(endpoints []*controlv1.Endpoint) []model.Endpoint {
	result := make([]model.Endpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint == nil {
			// 空指针 endpoint 直接跳过，保证状态转换过程更稳健。
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
