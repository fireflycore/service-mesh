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
	// --config 用于指定 YAML 文件入口；为空时直接走默认配置。
	cmd.Flags().StringVar(&opts.Path, "config", "", "config file path")
	// --mode 允许在不改文件的情况下切换 agent / sidecar。
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "runtime mode: agent or sidecar")
	// --source 允许快速切换 consul / etcd 目录来源。
	cmd.Flags().StringVar(&opts.SourceKind, "source", "", "source kind: consul or etcd")
	// --authz-target 用于覆盖 ext_authz 服务地址。
	cmd.Flags().StringVar(&opts.AuthzTarget, "authz-target", "", "ext_authz target")
	// --controlplane-target 用于覆盖控制面地址，便于本地联调。
	cmd.Flags().StringVar(&opts.ControlPlaneTarget, "controlplane-target", "", "control plane target")
}
