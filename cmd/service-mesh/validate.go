package main

import (
	"fmt"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/spf13/cobra"
)

// newValidateCmd 创建 validate 子命令。
//
// 它不启动运行时，
// 只用于提前发现配置错误和模式/来源组合错误。
func newValidateCmd() *cobra.Command {
	var opts config.LoadOptions

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate service-mesh config",
		RunE: func(cmd *cobra.Command, args []string) error {
			// validate 复用和 run 完全相同的加载链路，
			// 这样“校验通过”的配置就等价于“run 至少能完成启动前检查”。
			cfg, err := config.Load(opts)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config is valid: mode=%s source=%s\n", cfg.Mode, cfg.Source.Kind)
			return nil
		},
	}

	bindCommonFlags(cmd, &opts)
	return cmd
}
