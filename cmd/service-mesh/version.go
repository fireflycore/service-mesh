package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
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
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "version=%s commit=%s date=%s\n", version, commit, date)
			return err
		},
	}
}
