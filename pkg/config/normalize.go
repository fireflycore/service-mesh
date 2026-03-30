package config

import (
	"strings"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// Normalize 负责把用户输入修整成更稳定的内部配置形态。
//
// 这里会统一做：
// - 大小写与空白归一化
// - 默认值补齐
// - 保证后续 Validate 只关注“是否合法”
func Normalize(cfg *Config) {
	// mode/source kind 统一转小写，避免 CLI、环境变量、YAML 大小写混用。
	cfg.Mode = strings.TrimSpace(strings.ToLower(cfg.Mode))
	cfg.Source.Kind = strings.TrimSpace(strings.ToLower(cfg.Source.Kind))

	// 其余字符串类配置做 trim，避免首尾空格造成隐蔽错误。
	cfg.Runtime.Agent.Address = strings.TrimSpace(cfg.Runtime.Agent.Address)
	cfg.Runtime.Sidecar.Address = strings.TrimSpace(cfg.Runtime.Sidecar.Address)
	cfg.Runtime.Sidecar.TargetMode = strings.TrimSpace(strings.ToLower(cfg.Runtime.Sidecar.TargetMode))
	cfg.Runtime.Sidecar.ServiceName = strings.TrimSpace(cfg.Runtime.Sidecar.ServiceName)
	cfg.Runtime.Sidecar.InstanceID = strings.TrimSpace(cfg.Runtime.Sidecar.InstanceID)
	cfg.Runtime.Sidecar.Namespace = strings.TrimSpace(cfg.Runtime.Sidecar.Namespace)
	cfg.Runtime.Sidecar.Env = strings.TrimSpace(cfg.Runtime.Sidecar.Env)
	cfg.Authz.Target = strings.TrimSpace(cfg.Authz.Target)
	cfg.ControlPlane.Target = strings.TrimSpace(cfg.ControlPlane.Target)
	cfg.Source.Consul.Address = strings.TrimSpace(cfg.Source.Consul.Address)
	cfg.Source.Consul.Namespace = strings.TrimSpace(cfg.Source.Consul.Namespace)
	cfg.Source.Etcd.Namespace = strings.TrimSpace(cfg.Source.Etcd.Namespace)

	// 下面开始补齐数值型默认值。
	if cfg.Runtime.Agent.WorkerCount <= 0 {
		// 当前默认 4 个 worker，作为本地最小并发基线。
		cfg.Runtime.Agent.WorkerCount = 4
	}
	if cfg.Runtime.Agent.MaxInflight <= 0 {
		// 默认 inflight 保护值偏保守，优先保证本地实验可用。
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
	if cfg.Source.Consul.QueryTimeoutMS == 0 {
		// Consul 查询默认也限定 1 秒，避免本地没起 Consul 时长时间挂住。
		cfg.Source.Consul.QueryTimeoutMS = 1000
	}
	// 最后补齐字符串/切片默认值。
	if cfg.Runtime.Sidecar.ServiceName == "" {
		// service_name 缺省时给一个占位值，避免后续运行时装配拿到空字符串。
		cfg.Runtime.Sidecar.ServiceName = "service-mesh-sidecar"
	}
	if cfg.Runtime.Sidecar.TargetMode == "" {
		cfg.Runtime.Sidecar.TargetMode = model.SidecarTargetModeUpstreamOnly
	}
	if len(cfg.Invoke.RetryableCodes) == 0 {
		// 只给典型瞬时错误默认开启重试，避免对明确业务错误做无意义重试。
		cfg.Invoke.RetryableCodes = []string{
			"unavailable",
			"deadline_exceeded",
			"resource_exhausted",
		}
	}
}
