package config

type Config struct {
	Mode         string             `yaml:"mode"`
	Runtime      RuntimeConfig      `yaml:"runtime"`
	Invoke       InvokeConfig       `yaml:"invoke"`
	Source       SourceConfig       `yaml:"source"`
	Authz        AuthzConfig        `yaml:"authz"`
	ControlPlane ControlPlaneConfig `yaml:"controlplane"`
	Telemetry    TelemetryConfig    `yaml:"telemetry"`
}

type InvokeConfig struct {
	TimeoutMS        uint64   `yaml:"timeout_ms"`
	PerTryTimeoutMS  uint64   `yaml:"per_try_timeout_ms"`
	RetryMaxAttempts uint32   `yaml:"retry_max_attempts"`
	RetryBackoffMS   uint64   `yaml:"retry_backoff_ms"`
	RetryableCodes   []string `yaml:"retryable_codes"`
}

type RuntimeConfig struct {
	Agent   AgentRuntimeConfig   `yaml:"agent"`
	Sidecar SidecarRuntimeConfig `yaml:"sidecar"`
}

type AgentRuntimeConfig struct {
	Listen      ListenConfig `yaml:"listen"`
	WorkerCount int          `yaml:"worker_count"`
	MaxInflight int          `yaml:"max_inflight"`
}

type SidecarRuntimeConfig struct {
	Listen      ListenConfig `yaml:"listen"`
	ServiceName string       `yaml:"service_name"`
	InstanceID  string       `yaml:"instance_id"`
}

type ListenConfig struct {
	Network string `yaml:"network"`
	Address string `yaml:"address"`
}

type SourceConfig struct {
	Kind   string             `yaml:"kind"`
	Consul ConsulSourceConfig `yaml:"consul"`
	Etcd   EtcdSourceConfig   `yaml:"etcd"`
}

type ConsulSourceConfig struct {
	Address    string `yaml:"address"`
	Token      string `yaml:"token"`
	Namespace  string `yaml:"namespace"`
	Datacenter string `yaml:"datacenter"`
	Scheme     string `yaml:"scheme"`
}

type EtcdSourceConfig struct {
	Endpoints     []string `yaml:"endpoints"`
	Username      string   `yaml:"username"`
	Password      string   `yaml:"password"`
	Namespace     string   `yaml:"namespace"`
	DialTimeoutMS uint64   `yaml:"dial_timeout_ms"`
}

type AuthzConfig struct {
	Target         string   `yaml:"target"`
	TimeoutMS      uint64   `yaml:"timeout_ms"`
	FailOpen       bool     `yaml:"fail_open"`
	IncludeHeaders []string `yaml:"include_headers"`
}

type ControlPlaneConfig struct {
	Enabled             bool   `yaml:"enabled"`
	Target              string `yaml:"target"`
	HeartbeatIntervalMS uint64 `yaml:"heartbeat_interval_ms"`
	ConnectTimeoutMS    uint64 `yaml:"connect_timeout_ms"`
}

type TelemetryConfig struct {
	OTLPEndpoint  string `yaml:"otel_endpoint"`
	TraceEnabled  bool   `yaml:"trace_enabled"`
	MetricEnabled bool   `yaml:"metric_enabled"`
	LogEnabled    bool   `yaml:"log_enabled"`
}
