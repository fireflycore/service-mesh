package main

import (
	"os"

	"github.com/spf13/cobra"
)

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
