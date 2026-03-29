package main

import (
	"fmt"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var opts config.LoadOptions

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate service-mesh config",
		RunE: func(cmd *cobra.Command, args []string) error {
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
