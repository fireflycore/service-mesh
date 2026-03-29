package config

import (
	"errors"
	"fmt"

	serrs "github.com/fireflycore/service-mesh/pkg/errors"
	"github.com/fireflycore/service-mesh/pkg/model"
)

func Validate(cfg Config) error {
	switch cfg.Mode {
	case model.ModeAgent:
		if cfg.Runtime.Agent.Listen.Address == "" {
			return errors.New("runtime.agent.listen.address is required")
		}
	case model.ModeSidecar:
		if cfg.Runtime.Sidecar.Listen.Address == "" {
			return errors.New("runtime.sidecar.listen.address is required")
		}
		if cfg.Runtime.Sidecar.ServiceName == "" {
			return errors.New("runtime.sidecar.service_name is required")
		}
	default:
		return fmt.Errorf("%w: %s", serrs.ErrInvalidMode, cfg.Mode)
	}

	switch cfg.Source.Kind {
	case model.SourceConsul:
		if cfg.Source.Consul.Address == "" {
			return errors.New("source.consul.address is required")
		}
	case model.SourceEtcd:
		if len(cfg.Source.Etcd.Endpoints) == 0 {
			return errors.New("source.etcd.endpoints is required")
		}
	default:
		return fmt.Errorf("%w: %s", serrs.ErrInvalidSource, cfg.Source.Kind)
	}

	if cfg.Authz.Target == "" {
		return errors.New("authz.target is required")
	}
	if cfg.Invoke.PerTryTimeoutMS > cfg.Invoke.TimeoutMS {
		return errors.New("invoke.per_try_timeout_ms cannot be greater than invoke.timeout_ms")
	}
	if cfg.ControlPlane.Enabled && cfg.ControlPlane.Target == "" {
		return errors.New("controlplane.target is required when controlplane is enabled")
	}

	return nil
}
