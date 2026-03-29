package config

// Config 是 service-mesh 的根配置模型。
//
// 它把运行模式、目录来源、鉴权、控制面和观测等能力收敛到一个统一入口。
type Config struct {
	Mode         string             `yaml:"mode"`
	Runtime      RuntimeConfig      `yaml:"runtime"`
	Invoke       InvokeConfig       `yaml:"invoke"`
	Source       SourceConfig       `yaml:"source"`
	Authz        AuthzConfig        `yaml:"authz"`
	ControlPlane ControlPlaneConfig `yaml:"controlplane"`
	Telemetry    TelemetryConfig    `yaml:"telemetry"`
}

// InvokeConfig 描述一次调用的默认可靠性策略。
type InvokeConfig struct {
	TimeoutMS        uint64   `yaml:"timeout_ms"`
	PerTryTimeoutMS  uint64   `yaml:"per_try_timeout_ms"`
	RetryMaxAttempts uint32   `yaml:"retry_max_attempts"`
	RetryBackoffMS   uint64   `yaml:"retry_backoff_ms"`
	RetryableCodes   []string `yaml:"retryable_codes"`
}

// RuntimeConfig 聚合 agent / sidecar 两种运行模式的本地参数。
type RuntimeConfig struct {
	Agent   AgentRuntimeConfig   `yaml:"agent"`
	Sidecar SidecarRuntimeConfig `yaml:"sidecar"`
}

// AgentRuntimeConfig 描述 agent 模式的监听与容量参数。
type AgentRuntimeConfig struct {
	Listen      ListenConfig `yaml:"listen"`
	WorkerCount int          `yaml:"worker_count"`
	MaxInflight int          `yaml:"max_inflight"`
}

// SidecarRuntimeConfig 描述 sidecar 模式绑定的本地服务身份。
type SidecarRuntimeConfig struct {
	Listen      ListenConfig `yaml:"listen"`
	ServiceName string       `yaml:"service_name"`
	InstanceID  string       `yaml:"instance_id"`
	Namespace   string       `yaml:"namespace"`
	Env         string       `yaml:"env"`
}

// ListenConfig 统一承载本地监听协议与地址。
type ListenConfig struct {
	Network string `yaml:"network"`
	Address string `yaml:"address"`
}

// SourceConfig 描述目录来源选择。
type SourceConfig struct {
	Kind   string             `yaml:"kind"`
	Consul ConsulSourceConfig `yaml:"consul"`
	Etcd   EtcdSourceConfig   `yaml:"etcd"`
}

// ConsulSourceConfig 描述 Consul 目录连接参数。
type ConsulSourceConfig struct {
	Address    string `yaml:"address"`
	Token      string `yaml:"token"`
	Namespace  string `yaml:"namespace"`
	Datacenter string `yaml:"datacenter"`
	Scheme     string `yaml:"scheme"`
}

// EtcdSourceConfig 描述 etcd 目录连接参数。
type EtcdSourceConfig struct {
	Endpoints     []string `yaml:"endpoints"`
	Username      string   `yaml:"username"`
	Password      string   `yaml:"password"`
	Namespace     string   `yaml:"namespace"`
	DialTimeoutMS uint64   `yaml:"dial_timeout_ms"`
}

// AuthzConfig 描述 ext_authz 连接与失败策略。
type AuthzConfig struct {
	Target         string   `yaml:"target"`
	TimeoutMS      uint64   `yaml:"timeout_ms"`
	FailOpen       bool     `yaml:"fail_open"`
	IncludeHeaders []string `yaml:"include_headers"`
}

// ControlPlaneConfig 描述控制面连接与心跳参数。
type ControlPlaneConfig struct {
	Enabled             bool   `yaml:"enabled"`
	Target              string `yaml:"target"`
	HeartbeatIntervalMS uint64 `yaml:"heartbeat_interval_ms"`
	ConnectTimeoutMS    uint64 `yaml:"connect_timeout_ms"`
}

// TelemetryConfig 描述 OTel 基础开关与导出端点。
type TelemetryConfig struct {
	OTLPEndpoint  string `yaml:"otel_endpoint"`
	TraceEnabled  bool   `yaml:"trace_enabled"`
	MetricEnabled bool   `yaml:"metric_enabled"`
	LogEnabled    bool   `yaml:"log_enabled"`
}
