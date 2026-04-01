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
	otelintegration "github.com/fireflycore/service-mesh/integration/otel"
	"github.com/fireflycore/service-mesh/pkg/config"
	controlclient "github.com/fireflycore/service-mesh/plane/control/client"
	"github.com/fireflycore/service-mesh/plane/data/authz"
	"github.com/fireflycore/service-mesh/plane/data/balancer"
	"github.com/fireflycore/service-mesh/plane/data/invoke"
	"github.com/fireflycore/service-mesh/plane/data/resolver"
	meshtelemetry "github.com/fireflycore/service-mesh/plane/data/telemetry"
	"github.com/fireflycore/service-mesh/plane/data/transport"
	"github.com/fireflycore/service-mesh/source"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
)

// Params 描述 agent / sidecar 两类运行时的可变部分。
//
// shared runner 把“共性的 dataplane 装配链”集中起来，
// 再用 Params 表达不同模式之间的身份和监听差异。
type Params struct {
	// Mode 仅允许 agent / sidecar 两种值。
	Mode string
	// Address 定义本地 dataplane 服务暴露在哪个地址。
	Address string
	// ServiceName 决定注册到 controlplane 的逻辑服务名。
	ServiceName string
	// InstanceID 用于稳定标识单个 dataplane 实例。
	InstanceID string
	// Namespace/Env 用于让 sidecar 和控制面/目录服务语义对齐。
	Namespace string
	Env       string
	// TargetMode 只在 sidecar 模式下有意义，用于控制是否允许同名目标。
	TargetMode                      string
	TrustedOriginalIdentityInjector bool
	// LogAttributes 允许不同模式追加自己的结构化日志字段。
	LogAttributes []slog.Attr
}

// Runner 是 agent / sidecar 共用的运行时实现。
type Runner struct {
	// cfg 保存全局配置，主要用于运行阶段读取控制面与 source 设置。
	cfg *config.Config
	// params 保存当前模式专属的监听与身份差异。
	params   Params
	identity runtimeIdentity
	// grpcServer 是真正承载 MeshInvokeService 的本地服务端。
	grpcServer *grpc.Server
	// telemetry 管理全局 OTel provider 生命周期。
	telemetry *otelintegration.Providers
	// controlplaneClient 既负责长连接，也提供状态 overlay。
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
	identity := buildIdentity(cfg, params)
	// controlplane client 一方面负责 register/heartbeat，
	// 另一方面也把下发状态暴露给 dataplane 作为 overlay。
	controlplaneClient := controlclient.New(cfg.ControlPlane)
	provider, err := newProvider(cfg, controlplaneClient)
	if err != nil {
		return nil, err
	}

	// 当前鉴权统一走 ext_authz，shared runner 不区分 agent/sidecar。
	authorizer, err := authz.NewExtAuthz(cfg.Authz)
	if err != nil {
		// 鉴权 client 初始化失败时，当前运行时无法进入可服务状态。
		return nil, err
	}

	// telemetry provider 负责安装全局 OTel trace/meter provider。
	telemetryProviders, err := otelintegration.New(context.Background(), cfg.Telemetry, "service-mesh-"+params.Mode, identity.resourceAttributes()...)
	if err != nil {
		// telemetry 初始化失败当前直接终止启动，避免进入半可观测状态。
		return nil, err
	}

	// emitter 是 invoke 层真正使用的最小 telemetry 句柄集合。
	emitter, err := meshtelemetry.NewEmitter()
	if err != nil {
		// emitter 依赖全局 meter/tracer provider，失败时同样不继续启动。
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
			// 当前阶段 AppID 与 ServiceName 保持一致，后续如有独立 app_id 再拆分。
			AppID:                           params.ServiceName,
			Service:                         params.ServiceName,
			Namespace:                       strings.TrimSpace(params.Namespace),
			Env:                             strings.TrimSpace(params.Env),
			TargetMode:                      strings.TrimSpace(params.TargetMode),
			TrustedOriginalIdentityInjector: params.TrustedOriginalIdentityInjector,
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
		identity:           identity,
		grpcServer:         grpcServer,
		telemetry:          telemetryProviders,
		controlplaneClient: controlplaneClient,
	}, nil
}

func newProvider(cfg *config.Config, controlplaneClient *controlclient.Client) (source.Provider, error) {
	if !cfg.ControlPlane.Enabled {
		// 控制面关闭时，dataplane 直接回到本地 source 路径。
		return source.FromConfig(cfg.Source)
	}

	if cfg.ControlPlane.AllowSourceFallback {
		// 开发态或显式降级态下，控制面未命中时允许回退到底层 source。
		provider, err := source.FromConfig(cfg.Source)
		if err != nil {
			return nil, err
		}
		return source.NewOverlayWithFallback(provider, controlplaneClient, true), nil
	}

	// 第十四版默认让 controlplane 成为主路径，source 直连不再是常规调用路径。
	return source.NewOverlayWithFallback(nil, controlplaneClient, false), nil
}

// Run 负责真正启动本地 gRPC dataplane。
func (r *Runner) Run(ctx context.Context) error {
	defer func() {
		if r.telemetry != nil {
			// 无论是正常退出还是启动后失败，都尽量 flush trace/metric。
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = r.telemetry.Shutdown(shutdownCtx)
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
		// 这些字段是所有模式都会打印的最小运行时身份。
		slog.String("listen_address", r.params.Address),
		slog.String("mode", r.params.Mode),
		slog.String("dataplane_id", r.identity.DataplaneID),
		slog.String("node_id", r.identity.NodeID),
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
	// 这里不把 ctx.Err() 继续向上返回，避免正常停止被调用方误判成失败。
	slog.Info("service-mesh runtime stopped", slog.String("mode", r.params.Mode))
	return nil
}

// runControlPlane 在后台维持控制面连接。
func (r *Runner) runControlPlane(ctx context.Context) {
	if r.controlplaneClient == nil {
		return
	}

	// identity 是 dataplane 在控制面视角下的稳定身份。
	identity := r.identity.ControlPlane()

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
	address := r.params.Address

	listener, err := net.Listen("tcp", address)
	if err != nil {
		// bind 失败通常意味着地址占用、权限不足或 network/address 组合不合法。
		return nil, nil, err
	}

	cleanup := func() {
		_ = listener.Close()
	}

	return listener, cleanup, nil
}

type runtimeIdentity struct {
	DataplaneID string
	Mode        string
	NodeID      string
	Namespace   string
	Service     string
	Env         string
}

func buildIdentity(cfg *config.Config, params Params) runtimeIdentity {
	service := strings.TrimSpace(params.ServiceName)
	if service == "" {
		service = "service-mesh-" + params.Mode
	}

	nodeID := strings.TrimSpace(params.InstanceID)
	if nodeID == "" {
		nodeID = hostname()
	}

	dataplaneID := strings.TrimSpace(params.InstanceID)
	if dataplaneID == "" {
		dataplaneID = nodeID + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	namespace := strings.TrimSpace(params.Namespace)
	if namespace == "" {
		switch cfg.Source.Kind {
		case "etcd":
			namespace = cfg.Source.Etcd.Namespace
		default:
			namespace = cfg.Source.Consul.Namespace
		}
	}

	return runtimeIdentity{
		DataplaneID: dataplaneID,
		Mode:        params.Mode,
		NodeID:      nodeID,
		Namespace:   strings.TrimSpace(namespace),
		Service:     service,
		Env:         strings.TrimSpace(params.Env),
	}
}

func (i runtimeIdentity) ControlPlane() *controlv1.DataplaneIdentity {
	return &controlv1.DataplaneIdentity{
		DataplaneId: i.DataplaneID,
		Mode:        i.Mode,
		NodeId:      i.NodeID,
		Namespace:   i.Namespace,
		Service:     i.Service,
		Env:         i.Env,
	}
}

func (i runtimeIdentity) resourceAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("mesh.mode", i.Mode),
		attribute.String("mesh.dataplane_id", i.DataplaneID),
		attribute.String("mesh.node_id", i.NodeID),
		attribute.String("mesh.namespace", i.Namespace),
		attribute.String("mesh.service", i.Service),
		attribute.String("mesh.env", i.Env),
	}
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
