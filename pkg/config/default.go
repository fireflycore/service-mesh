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
		Mode: model.ModeAgent,
		Runtime: RuntimeConfig{
			Agent: AgentRuntimeConfig{
				Listen: ListenConfig{
					Network: "unix",
					Address: "/var/run/service-mesh.sock",
				},
				WorkerCount: 4,
				MaxInflight: 1024,
			},
			Sidecar: SidecarRuntimeConfig{
				Listen: ListenConfig{
					Network: "tcp",
					Address: "127.0.0.1:19090",
				},
				ServiceName: "service-mesh-sidecar",
			},
		},
		Invoke: InvokeConfig{
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
			Target:    "127.0.0.1:9001",
			TimeoutMS: 500,
		},
		ControlPlane: ControlPlaneConfig{
			Enabled:             true,
			Target:              "127.0.0.1:19080",
			HeartbeatIntervalMS: 3000,
			ConnectTimeoutMS:    1000,
		},
		Telemetry: TelemetryConfig{
			OTLPEndpoint:  "http://127.0.0.1:4318",
			TraceEnabled:  true,
			MetricEnabled: true,
			LogEnabled:    true,
		},
	}
}
