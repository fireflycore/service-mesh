package main

import (
	"os"

	"github.com/spf13/cobra"
)

// newRootCmd 创建 service-mesh CLI 根命令。
//
// 根命令本身不执行实际业务，
// 只负责挂载各个子命令并统一 stdout / stderr。
func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service-mesh",
		Short: "Firefly Service Mesh runtime",
	}

	cmd.AddCommand(
		newRunCmd(),
		newValidateCmd(),
		newPrintConfigCmd(),
		newVersionCmd(),
	)

	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	return cmd
}
