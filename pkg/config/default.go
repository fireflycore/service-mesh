package config

import "github.com/fireflycore/service-mesh/pkg/model"

// Default 返回一份可直接运行的默认配置。
//
// 默认值的原则是：
// - 本地开发优先
// - 能覆盖 agent / sidecar 两种模式
// - 对 MVP 主链路保持最小可用
func Default() Config {
	return Config{
		// 默认先从 agent 模式启动，因为它更适合本地共享入口。
		Mode: model.ModeAgent,
		Runtime: RuntimeConfig{
			Agent: AgentRuntimeConfig{
				// agent 默认使用本地 TCP 地址，和当前网格接入策略保持一致。
				Address:     "127.0.0.1:19090",
				WorkerCount: 4,
				MaxInflight: 1024,
			},
			Sidecar: SidecarRuntimeConfig{
				// sidecar 默认使用本地 TCP 地址，便于容器内或进程内联调。
				Address: "127.0.0.1:19090",
				// 默认只允许 sidecar 代理“本地服务 -> 上游服务”。
				TargetMode: model.SidecarTargetModeUpstreamOnly,
				// 给 sidecar 一个最小占位服务名，避免配置完全缺失时无法 Normalize。
				ServiceName: "service-mesh-sidecar",
			},
		},
		Invoke: InvokeConfig{
			// 1500ms 总预算 + 500ms 单次尝试，是当前 MVP 比较保守的默认值。
			TimeoutMS:        1500,
			PerTryTimeoutMS:  500,
			RetryMaxAttempts: 2,
			RetryBackoffMS:   50,
			RetryableCodes: []string{
				"unavailable",
				"deadline_exceeded",
				"resource_exhausted",
			},
		},
		Source: SourceConfig{
			// 默认走 consul，是因为早期本地环境更常见。
			Kind: model.SourceConsul,
			Consul: ConsulSourceConfig{
				Address:   "127.0.0.1:8500",
				Namespace: "/microservice/lhdht",
				Scheme:    "http",
			},
			Etcd: EtcdSourceConfig{
				Endpoints:     []string{"127.0.0.1:2379"},
				Namespace:     "/microservice/lhdht",
				DialTimeoutMS: 1000,
			},
		},
		Authz: AuthzConfig{
			// 鉴权默认开启并指向本地 ext_authz。
			Target:    "127.0.0.1:9001",
			TimeoutMS: 500,
		},
		ControlPlane: ControlPlaneConfig{
			// 控制面默认开启，便于一开始就验证 register / heartbeat 主链。
			Enabled: true,
			Target:  "127.0.0.1:19080",
			// 第十四版开始默认走 control plane 主路径，本地 source 只保留显式降级时使用。
			AllowSourceFallback: false,
			HeartbeatIntervalMS: 3000,
			ConnectTimeoutMS:    1000,
		},
		Telemetry: TelemetryConfig{
			// OTel 默认全开，但是否成功导出仍取决于本地 collector 是否存在。
			OTLPEndpoint:  "http://127.0.0.1:4318",
			TraceEnabled:  true,
			MetricEnabled: true,
			LogEnabled:    true,
		},
	}
}
