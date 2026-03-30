package config

import (
	"errors"
	"fmt"

	serrs "github.com/fireflycore/service-mesh/pkg/errors"
	"github.com/fireflycore/service-mesh/pkg/model"
)

// Validate 负责检查规范化后的配置是否满足运行条件。
func Validate(cfg Config) error {
	switch cfg.Mode {
	case model.ModeAgent:
		// agent 只要求本地监听地址存在，其余容量参数允许走默认值。
		if cfg.Runtime.Agent.Listen.Address == "" {
			return errors.New("runtime.agent.listen.address is required")
		}
	case model.ModeSidecar:
		// sidecar 不仅要能监听，还必须知道自己绑定的是哪个本地服务。
		if cfg.Runtime.Sidecar.Listen.Address == "" {
			return errors.New("runtime.sidecar.listen.address is required")
		}
		if cfg.Runtime.Sidecar.Listen.Network != "tcp" {
			return errors.New("runtime.sidecar.listen.network must be tcp")
		}
		switch cfg.Runtime.Sidecar.TargetMode {
		case model.SidecarTargetModeUpstreamOnly, model.SidecarTargetModeAllowSameService:
		default:
			return errors.New("runtime.sidecar.target_mode must be upstream_only or allow_same_service")
		}
		if cfg.Runtime.Sidecar.ServiceName == "" {
			return errors.New("runtime.sidecar.service_name is required")
		}
	default:
		return fmt.Errorf("%w: %s", serrs.ErrInvalidMode, cfg.Mode)
	}

	switch cfg.Source.Kind {
	case model.SourceConsul:
		// Consul 模式至少需要知道要连哪一个 Consul 集群。
		if cfg.Source.Consul.Address == "" {
			return errors.New("source.consul.address is required")
		}
	case model.SourceEtcd:
		// etcd 模式至少需要有一个 endpoint，后续客户端才能真正建链。
		if len(cfg.Source.Etcd.Endpoints) == 0 {
			return errors.New("source.etcd.endpoints is required")
		}
	default:
		return fmt.Errorf("%w: %s", serrs.ErrInvalidSource, cfg.Source.Kind)
	}

	// 当前实现要求 ext_authz 总是可配置；真正是否放行由 fail_open 决定。
	if cfg.Authz.Target == "" {
		return errors.New("authz.target is required")
	}
	// 单次尝试不能大于整次调用预算，否则超时语义会前后矛盾。
	if cfg.Invoke.PerTryTimeoutMS > cfg.Invoke.TimeoutMS {
		return errors.New("invoke.per_try_timeout_ms cannot be greater than invoke.timeout_ms")
	}
	// 控制面只要启用，就必须能给出明确连接目标。
	if cfg.ControlPlane.Enabled && cfg.ControlPlane.Target == "" {
		return errors.New("controlplane.target is required when controlplane is enabled")
	}

	// 其余字段当前没有额外硬约束，交给运行时默认值和具体组件处理。
	return nil
}
