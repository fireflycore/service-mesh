package main

import (
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/spf13/cobra"
)

// bindCommonFlags 把多个子命令共享的配置入口统一绑定到 LoadOptions。
//
// 这里刻意只暴露最小必要覆盖项，
// 更复杂的参数仍然建议通过 YAML 配置文件提供。
func bindCommonFlags(cmd *cobra.Command, opts *config.LoadOptions) {
	cmd.Flags().StringVar(&opts.Path, "config", "", "config file path")
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "runtime mode: agent or sidecar")
	cmd.Flags().StringVar(&opts.SourceKind, "source", "", "source kind: consul or etcd")
	cmd.Flags().StringVar(&opts.AuthzTarget, "authz-target", "", "ext_authz target")
	cmd.Flags().StringVar(&opts.ControlPlaneTarget, "controlplane-target", "", "control plane target")
}
