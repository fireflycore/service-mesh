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
			// 这里输出的是“最终生效配置”，
			// 不是原始 YAML 文件内容。
			cfg, err := config.Load(opts)
			if err != nil {
				return err
			}
			// Render 会把结构体重新编码成更适合查看的 YAML。
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
