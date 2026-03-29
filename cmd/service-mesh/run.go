package main

import (
	"context"
	"os/signal"
	"syscall"

	servicemeshapp "github.com/fireflycore/service-mesh/app/service-mesh"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/spf13/cobra"
)

// newRunCmd 创建 run 子命令。
//
// 它负责：
// - 读取并校验配置
// - 构造 App
// - 绑定进程信号，优雅退出 runtime
func newRunCmd() *cobra.Command {
	var opts config.LoadOptions

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run service-mesh runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load 内部会依次执行：
			// 默认值 -> 文件 -> 环境变量 -> CLI 覆盖 -> Normalize -> Validate。
			cfg, err := config.Load(opts)
			if err != nil {
				return err
			}

			// 顶层 App 负责再往下选择 agent / sidecar 具体运行时。
			app, err := servicemeshapp.New(cfg)
			if err != nil {
				return err
			}

			// 用进程信号驱动上下文取消，保证 Ctrl+C 时能优雅退出 gRPC server 等资源。
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return app.Run(ctx)
		},
	}

	bindCommonFlags(cmd, &opts)
	return cmd
}
