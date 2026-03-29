package shared

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	controlclient "github.com/fireflycore/service-mesh/controlplane/client"
	"github.com/fireflycore/service-mesh/dataplane/authz"
	"github.com/fireflycore/service-mesh/dataplane/balancer"
	"github.com/fireflycore/service-mesh/dataplane/invoke"
	"github.com/fireflycore/service-mesh/dataplane/resolver"
	meshtelemetry "github.com/fireflycore/service-mesh/dataplane/telemetry"
	"github.com/fireflycore/service-mesh/dataplane/transport"
	otelintegration "github.com/fireflycore/service-mesh/integration/otel"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/source"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// Params 描述 agent / sidecar 两类运行时的可变部分。
//
// shared runner 把“共性的 dataplane 装配链”集中起来，
// 再用 Params 表达不同模式之间的身份和监听差异。
type Params struct {
	// Mode 仅允许 agent / sidecar 两种值。
	Mode string
	// Listen 定义本地 dataplane 服务暴露在哪个地址。
	Listen config.ListenConfig
	// ServiceName 决定注册到 controlplane 的逻辑服务名。
	ServiceName string
	// InstanceID 用于稳定标识单个 dataplane 实例。
	InstanceID string
	// Namespace/Env 用于让 sidecar 和控制面/目录服务语义对齐。
	Namespace string
	Env       string
	// LogAttributes 允许不同模式追加自己的结构化日志字段。
	LogAttributes []slog.Attr
}

// Runner 是 agent / sidecar 共用的运行时实现。
type Runner struct {
	cfg                *config.Config
	params             Params
	grpcServer         *grpc.Server
	telemetry          *otelintegration.Providers
	controlplaneClient *controlclient.Client
}

// New 装配共享运行时。
//
// 它统一串起：
// - source / overlay
// - authz
// - invoke service
// - controlplane client
// - telemetry
func New(cfg *config.Config, params Params) (*Runner, error) {
	// 先根据配置选择底层目录实现，例如 consul 或 etcd。
	provider, err := source.FromConfig(cfg.Source)
	if err != nil {
		return nil, err
	}

	// controlplane client 一方面负责 register/heartbeat，
	// 另一方面也把下发状态暴露给 dataplane 作为 overlay。
	controlplaneClient := controlclient.New(cfg.ControlPlane)
	provider = source.NewOverlay(provider, controlplaneClient.State())

	// 当前鉴权统一走 ext_authz，shared runner 不区分 agent/sidecar。
	authorizer, err := authz.NewExtAuthz(cfg.Authz)
	if err != nil {
		return nil, err
	}

	// telemetry provider 负责安装全局 OTel trace/meter provider。
	telemetryProviders, err := otelintegration.New(context.Background(), cfg.Telemetry, "service-mesh-"+params.Mode)
	if err != nil {
		return nil, err
	}

	// emitter 是 invoke 层真正使用的最小 telemetry 句柄集合。
	emitter, err := meshtelemetry.NewEmitter()
	if err != nil {
		return nil, err
	}

	// 先从静态配置生成 invoke 选项，再叠加 runtime 级别的动态来源。
	invokeOptions := invoke.OptionsFromConfig(cfg.Invoke)
	invokeOptions.Telemetry = emitter
	invokeOptions.PolicySource = controlplaneClient.State()
	if params.Mode == "sidecar" {
		// sidecar 会把自己的本地服务身份注入到 invoke 入口，
		// 这样未显式填写 caller/target 维度时也能得到稳定默认值。
		invokeOptions.LocalIdentity = &invoke.LocalIdentity{
			AppID:     params.ServiceName,
			Service:   params.ServiceName,
			Namespace: strings.TrimSpace(params.Namespace),
			Env:       strings.TrimSpace(params.Env),
		}
	}

	// resolver 统一负责“目录查询 + 负载均衡选点”。
	invokeService := invoke.NewService(
		authorizer,
		resolver.New(provider, balancer.NewRoundRobin()),
		transport.NewGRPC(),
		invokeOptions,
	)

	// 本地 gRPC server 只暴露 MeshInvokeService，一个运行时一个入口。
	grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	invokev1.RegisterMeshInvokeServiceServer(grpcServer, invokeService)

	return &Runner{
		cfg:                cfg,
		params:             params,
		grpcServer:         grpcServer,
		telemetry:          telemetryProviders,
		controlplaneClient: controlplaneClient,
	}, nil
}

// Run 负责真正启动本地 gRPC dataplane。
func (r *Runner) Run(ctx context.Context) error {
	defer func() {
		if r.telemetry != nil {
			// 无论是正常退出还是启动后失败，都尽量 flush trace/metric。
			_ = r.telemetry.Shutdown(context.Background())
		}
	}()

	// listener 放在 Run 阶段创建，避免 New 阶段产生外部副作用。
	listener, cleanup, err := r.listen()
	if err != nil {
		return err
	}
	defer cleanup()

	// 用单独 goroutine 挂载 gRPC serve，主 goroutine 负责等待退出信号。
	errCh := make(chan error, 1)
	go func() {
		if serveErr := r.grpcServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			errCh <- serveErr
		}
		close(errCh)
	}()

	if r.cfg.ControlPlane.Enabled {
		// 控制面连接放后台维持，不阻塞本地 dataplane 启动。
		go r.runControlPlane(ctx)
	}

	attrs := []slog.Attr{
		slog.String("listen_network", r.params.Listen.Network),
		slog.String("listen_address", r.params.Listen.Address),
		slog.String("mode", r.params.Mode),
	}
	attrs = append(attrs, r.params.LogAttributes...)
	slog.LogAttrs(ctx, slog.LevelInfo, "service-mesh runtime started", attrs...)

	select {
	case err := <-errCh:
		// 如果 gRPC server 异常退出，要把错误直接抛给上层。
		if err != nil {
			return err
		}
	case <-ctx.Done():
		// 外部取消通常来自进程信号或测试主动停止。
	}

	// GracefulStop 会优先等待现有请求处理完成。
	r.grpcServer.GracefulStop()
	slog.Info("service-mesh runtime stopped", slog.String("mode", r.params.Mode))
	return nil
}

// runControlPlane 在后台维持控制面连接。
func (r *Runner) runControlPlane(ctx context.Context) {
	if r.controlplaneClient == nil {
		return
	}

	// identity 是 dataplane 在控制面视角下的稳定身份。
	identity := &controlv1.DataplaneIdentity{
		DataplaneId: r.dataplaneID(),
		Mode:        r.params.Mode,
		NodeId:      r.nodeID(),
		Namespace:   r.namespace(),
		Service:     r.serviceName(),
		Env:         r.env(),
	}

	// 这里把 context.Canceled 排除掉，避免正常退出时刷无意义 warning。
	if err := r.controlplaneClient.Run(ctx, identity); err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("controlplane client stopped",
			slog.String("target", r.cfg.ControlPlane.Target),
			slog.String("mode", r.params.Mode),
			slog.String("error", err.Error()),
		)
	}
}

// listen 根据当前 mode 的监听配置创建 listener。
func (r *Runner) listen() (net.Listener, func(), error) {
	network := r.params.Listen.Network
	address := r.params.Listen.Address

	if network == "unix" {
		// UDS 模式下清掉旧 sock，避免上次异常退出残留文件导致 bind 失败。
		if err := os.RemoveAll(address); err != nil {
			return nil, nil, err
		}
	}

	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		_ = listener.Close()
		if network == "unix" {
			// 正常退出时顺手清理 sock 文件，避免影响下一次启动。
			_ = os.Remove(address)
		}
	}

	return listener, cleanup, nil
}

// dataplaneID 优先使用外部明确提供的实例标识。
func (r *Runner) dataplaneID() string {
	if strings.TrimSpace(r.params.InstanceID) != "" {
		// sidecar 一般更希望复用业务实例 ID，而不是生成临时值。
		return r.params.InstanceID
	}
	// agent 缺少显式实例 ID 时，用 hostname + 时间戳 生成本地唯一值。
	return hostname() + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// serviceName 返回当前运行时注册到 controlplane 的逻辑服务名。
func (r *Runner) serviceName() string {
	if strings.TrimSpace(r.params.ServiceName) != "" {
		return r.params.ServiceName
	}
	// 理论上大多数场景都会显式指定；这里只保留兜底命名。
	return "service-mesh-" + r.params.Mode
}

// nodeID 返回当前进程所在节点或实例标识。
func (r *Runner) nodeID() string {
	if strings.TrimSpace(r.params.InstanceID) != "" {
		// sidecar 绑定到具体实例时，nodeID 也沿用实例 ID，便于日志关联。
		return r.params.InstanceID
	}
	return hostname()
}

// namespace 优先使用运行时显式提供的业务命名空间。
func (r *Runner) namespace() string {
	if strings.TrimSpace(r.params.Namespace) != "" {
		// sidecar 更倾向于使用和本地业务身份绑定的 namespace。
		return r.params.Namespace
	}
	switch r.cfg.Source.Kind {
	case "etcd":
		// etcd 场景下，注册与目录解析通常共用同一个前缀 namespace。
		return r.cfg.Source.Etcd.Namespace
	default:
		return r.cfg.Source.Consul.Namespace
	}
}

// env 返回当前运行时所在环境。
func (r *Runner) env() string {
	return strings.TrimSpace(r.params.Env)
}

// hostname 是 shared runner 中统一的主机名读取入口。
func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		// 这里宁可回退占位值，也不让控制面身份生成失败。
		return "unknown-node"
	}
	return name
}
