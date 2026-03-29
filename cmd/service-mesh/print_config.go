package main

import (
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/spf13/cobra"
)

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
