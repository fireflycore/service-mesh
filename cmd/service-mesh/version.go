package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// 这三个值默认是本地开发占位值；
	// 正式构建时通常通过 -ldflags 覆盖。
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// newVersionCmd 创建 version 子命令。
//
// 这三个变量通常在构建阶段由 ldflags 注入，
// 本地开发时会保留默认占位值。
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print service-mesh version",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 输出格式保持单行，便于脚本解析。
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "version=%s commit=%s date=%s\n", version, commit, date)
			return err
		},
	}
}
