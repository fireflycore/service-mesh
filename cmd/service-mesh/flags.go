package main

import (
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/spf13/cobra"
)

func bindCommonFlags(cmd *cobra.Command, opts *config.LoadOptions) {
	cmd.Flags().StringVar(&opts.Path, "config", "", "config file path")
	cmd.Flags().StringVar(&opts.Mode, "mode", "", "runtime mode: agent or sidecar")
	cmd.Flags().StringVar(&opts.SourceKind, "source", "", "source kind: consul or etcd")
	cmd.Flags().StringVar(&opts.AuthzTarget, "authz-target", "", "ext_authz target")
	cmd.Flags().StringVar(&opts.ControlPlaneTarget, "controlplane-target", "", "control plane target")
}
