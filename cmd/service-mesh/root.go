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

	// 所有子命令都在这里集中注册，方便后续统一扩展 CLI 能力。
	cmd.AddCommand(
		newRunCmd(),
		newValidateCmd(),
		newPrintConfigCmd(),
		newVersionCmd(),
	)

	// 明确把输出流绑定到当前进程标准输出，便于 shell 管道和测试接管。
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	return cmd
}
