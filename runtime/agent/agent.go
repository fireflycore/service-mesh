package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/dataplane/authz"
	"github.com/fireflycore/service-mesh/dataplane/balancer"
	"github.com/fireflycore/service-mesh/dataplane/invoke"
	"github.com/fireflycore/service-mesh/dataplane/resolver"
	"github.com/fireflycore/service-mesh/dataplane/transport"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/source"
	"google.golang.org/grpc"
)

type Runner struct {
	cfg        *config.Config
	grpcServer *grpc.Server
}

// New 创建 agent 运行时，并把当前版本需要的核心依赖装配起来。
//
// 当前第三版装配链路是：
// - source 根据配置选择目录来源
// - resolver 基于 source + balancer 解析目标实例
// - invoke service 作为本地 gRPC 入口
// - transport 负责把请求真正发到下游
func New(cfg *config.Config) (*Runner, error) {
	provider, err := source.FromConfig(cfg.Source)
	if err != nil {
		return nil, err
	}

	invokeService := invoke.NewService(
		authz.NewAllowAll(),
		resolver.New(provider, balancer.NewRoundRobin()),
		transport.NewGRPC(),
	)

	grpcServer := grpc.NewServer()
	invokev1.RegisterMeshInvokeServiceServer(grpcServer, invokeService)

	return &Runner{
		cfg:        cfg,
		grpcServer: grpcServer,
	}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	// listen 在 Run 内执行，而不是在 New 时执行。
	//
	// 这样可以确保：
	// - 构造对象阶段不产生副作用
	// - 测试和 CLI 校验阶段不会提前占用端口或创建 UDS 文件
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

	slog.Info("service-mesh agent started",
		slog.String("listen_network", r.cfg.Runtime.Agent.Listen.Network),
		slog.String("listen_address", r.cfg.Runtime.Agent.Listen.Address),
		slog.String("source_kind", r.cfg.Source.Kind),
		slog.String("authz_target", r.cfg.Authz.Target),
	)

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
	}

	r.grpcServer.GracefulStop()
	slog.Info("service-mesh agent stopped")
	return nil
}

func (r *Runner) listen() (net.Listener, func(), error) {
	network := r.cfg.Runtime.Agent.Listen.Network
	address := r.cfg.Runtime.Agent.Listen.Address

	if network == "unix" {
		// UDS 模式下需要先清理旧 sock 文件。
		//
		// 否则上一次异常退出留下的文件会导致本次启动直接失败。
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
