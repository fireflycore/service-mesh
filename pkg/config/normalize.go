package config

import "strings"

// Normalize 负责把用户输入修整成更稳定的内部配置形态。
//
// 这里会统一做：
// - 大小写与空白归一化
// - 默认值补齐
// - 保证后续 Validate 只关注“是否合法”
func Normalize(cfg *Config) {
	cfg.Mode = strings.TrimSpace(strings.ToLower(cfg.Mode))
	cfg.Source.Kind = strings.TrimSpace(strings.ToLower(cfg.Source.Kind))

	cfg.Runtime.Agent.Listen.Network = strings.TrimSpace(strings.ToLower(cfg.Runtime.Agent.Listen.Network))
	cfg.Runtime.Sidecar.Listen.Network = strings.TrimSpace(strings.ToLower(cfg.Runtime.Sidecar.Listen.Network))

	cfg.Runtime.Agent.Listen.Address = strings.TrimSpace(cfg.Runtime.Agent.Listen.Address)
	cfg.Runtime.Sidecar.Listen.Address = strings.TrimSpace(cfg.Runtime.Sidecar.Listen.Address)
	cfg.Runtime.Sidecar.ServiceName = strings.TrimSpace(cfg.Runtime.Sidecar.ServiceName)
	cfg.Runtime.Sidecar.InstanceID = strings.TrimSpace(cfg.Runtime.Sidecar.InstanceID)
	cfg.Runtime.Sidecar.Namespace = strings.TrimSpace(cfg.Runtime.Sidecar.Namespace)
	cfg.Runtime.Sidecar.Env = strings.TrimSpace(cfg.Runtime.Sidecar.Env)
	cfg.Authz.Target = strings.TrimSpace(cfg.Authz.Target)
	cfg.ControlPlane.Target = strings.TrimSpace(cfg.ControlPlane.Target)
	cfg.Source.Consul.Address = strings.TrimSpace(cfg.Source.Consul.Address)
	cfg.Source.Consul.Namespace = strings.TrimSpace(cfg.Source.Consul.Namespace)
	cfg.Source.Etcd.Namespace = strings.TrimSpace(cfg.Source.Etcd.Namespace)

	if cfg.Runtime.Agent.WorkerCount <= 0 {
		cfg.Runtime.Agent.WorkerCount = 4
	}
	if cfg.Runtime.Agent.MaxInflight <= 0 {
		cfg.Runtime.Agent.MaxInflight = 1024
	}
	if cfg.Authz.TimeoutMS == 0 {
		cfg.Authz.TimeoutMS = 500
	}
	if cfg.Invoke.TimeoutMS == 0 {
		cfg.Invoke.TimeoutMS = 1500
	}
	if cfg.Invoke.PerTryTimeoutMS == 0 {
		cfg.Invoke.PerTryTimeoutMS = 500
	}
	if cfg.Invoke.RetryMaxAttempts == 0 {
		cfg.Invoke.RetryMaxAttempts = 2
	}
	if cfg.Invoke.RetryBackoffMS == 0 {
		cfg.Invoke.RetryBackoffMS = 50
	}
	if cfg.ControlPlane.HeartbeatIntervalMS == 0 {
		cfg.ControlPlane.HeartbeatIntervalMS = 3000
	}
	if cfg.ControlPlane.ConnectTimeoutMS == 0 {
		cfg.ControlPlane.ConnectTimeoutMS = 1000
	}
	if cfg.Source.Etcd.DialTimeoutMS == 0 {
		cfg.Source.Etcd.DialTimeoutMS = 1000
	}
	if cfg.Runtime.Agent.Listen.Network == "" {
		cfg.Runtime.Agent.Listen.Network = "unix"
	}
	if cfg.Runtime.Sidecar.Listen.Network == "" {
		cfg.Runtime.Sidecar.Listen.Network = "tcp"
	}
	if cfg.Runtime.Sidecar.ServiceName == "" {
		cfg.Runtime.Sidecar.ServiceName = "service-mesh-sidecar"
	}
	if len(cfg.Invoke.RetryableCodes) == 0 {
		cfg.Invoke.RetryableCodes = []string{
			"unavailable",
			"deadline_exceeded",
			"resource_exhausted",
		}
	}
}
