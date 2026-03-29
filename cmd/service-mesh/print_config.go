package main

import (
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/spf13/cobra"
)

// newPrintConfigCmd 创建 print-config 子命令。
//
// 它会输出已经过默认值补齐和规范化处理后的配置，
// 便于检查最终运行时真正看到的内容。
func newPrintConfigCmd() *cobra.Command {
	var opts config.LoadOptions

	cmd := &cobra.Command{
		Use:   "print-config",
		Short: "Print normalized service-mesh config",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(opts)
			if err != nil {
				return err
			}
			raw, err := config.Render(*cfg)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(raw)
			return err
		},
	}

	bindCommonFlags(cmd, &opts)
	return cmd
}
