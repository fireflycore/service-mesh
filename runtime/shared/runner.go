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
	Mode          string
	Listen        config.ListenConfig
	ServiceName   string
	InstanceID    string
	Namespace     string
	Env           string
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
	provider, err := source.FromConfig(cfg.Source)
	if err != nil {
		return nil, err
	}

	controlplaneClient := controlclient.New(cfg.ControlPlane)
	provider = source.NewOverlay(provider, controlplaneClient.State())

	authorizer, err := authz.NewExtAuthz(cfg.Authz)
	if err != nil {
		return nil, err
	}

	telemetryProviders, err := otelintegration.New(context.Background(), cfg.Telemetry, "service-mesh-"+params.Mode)
	if err != nil {
		return nil, err
	}

	emitter, err := meshtelemetry.NewEmitter()
	if err != nil {
		return nil, err
	}

	invokeOptions := invoke.OptionsFromConfig(cfg.Invoke)
	invokeOptions.Telemetry = emitter
	invokeOptions.PolicySource = controlplaneClient.State()
	if params.Mode == "sidecar" {
		invokeOptions.LocalIdentity = &invoke.LocalIdentity{
			AppID:     params.ServiceName,
			Service:   params.ServiceName,
			Namespace: strings.TrimSpace(params.Namespace),
			Env:       strings.TrimSpace(params.Env),
		}
	}

	invokeService := invoke.NewService(
		authorizer,
		resolver.New(provider, balancer.NewRoundRobin()),
		transport.NewGRPC(),
		invokeOptions,
	)

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
			_ = r.telemetry.Shutdown(context.Background())
		}
	}()

	listener, cleanup, err := r.listen()
	if err != nil {
		return err
	}
	defer cleanup()

	errCh := make(chan error, 1)
	go func() {
		if serveErr := r.grpcServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			errCh <- serveErr
		}
		close(errCh)
	}()

	if r.cfg.ControlPlane.Enabled {
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
		if err != nil {
			return err
		}
	case <-ctx.Done():
	}

	r.grpcServer.GracefulStop()
	slog.Info("service-mesh runtime stopped", slog.String("mode", r.params.Mode))
	return nil
}

// runControlPlane 在后台维持控制面连接。
func (r *Runner) runControlPlane(ctx context.Context) {
	if r.controlplaneClient == nil {
		return
	}

	identity := &controlv1.DataplaneIdentity{
		DataplaneId: r.dataplaneID(),
		Mode:        r.params.Mode,
		NodeId:      r.nodeID(),
		Namespace:   r.namespace(),
		Service:     r.serviceName(),
		Env:         r.env(),
	}

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
			_ = os.Remove(address)
		}
	}

	return listener, cleanup, nil
}

// dataplaneID 优先使用外部明确提供的实例标识。
func (r *Runner) dataplaneID() string {
	if strings.TrimSpace(r.params.InstanceID) != "" {
		return r.params.InstanceID
	}
	return hostname() + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// serviceName 返回当前运行时注册到 controlplane 的逻辑服务名。
func (r *Runner) serviceName() string {
	if strings.TrimSpace(r.params.ServiceName) != "" {
		return r.params.ServiceName
	}
	return "service-mesh-" + r.params.Mode
}

// nodeID 返回当前进程所在节点或实例标识。
func (r *Runner) nodeID() string {
	if strings.TrimSpace(r.params.InstanceID) != "" {
		return r.params.InstanceID
	}
	return hostname()
}

// namespace 优先使用运行时显式提供的业务命名空间。
func (r *Runner) namespace() string {
	if strings.TrimSpace(r.params.Namespace) != "" {
		return r.params.Namespace
	}
	switch r.cfg.Source.Kind {
	case "etcd":
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
		return "unknown-node"
	}
	return name
}
