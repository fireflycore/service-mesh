package config

// Config 是 service-mesh 的根配置模型。
//
// 它把运行模式、目录来源、鉴权、控制面和观测等能力收敛到一个统一入口。
type Config struct {
	// Mode 决定当前进程以 agent 还是 sidecar 运行。
	Mode string `yaml:"mode"`
	// Runtime 保存本地监听与运行时容量等参数。
	Runtime RuntimeConfig `yaml:"runtime"`
	// Invoke 保存默认的超时与重试策略。
	Invoke InvokeConfig `yaml:"invoke"`
	// Source 决定服务目录来自 consul 还是 etcd。
	Source SourceConfig `yaml:"source"`
	// Authz 定义外部鉴权服务连接信息。
	Authz AuthzConfig `yaml:"authz"`
	// ControlPlane 定义集中控制面的连接开关与心跳参数。
	ControlPlane ControlPlaneConfig `yaml:"controlplane"`
	// Telemetry 定义 OTel 导出端点与功能开关。
	Telemetry TelemetryConfig `yaml:"telemetry"`
}

// InvokeConfig 描述一次调用的默认可靠性策略。
type InvokeConfig struct {
	// TimeoutMS 是单次调用的总超时预算。
	TimeoutMS uint64 `yaml:"timeout_ms"`
	// PerTryTimeoutMS 是每次尝试各自的超时预算。
	PerTryTimeoutMS uint64 `yaml:"per_try_timeout_ms"`
	// RetryMaxAttempts 包含第一次尝试。
	RetryMaxAttempts uint32 `yaml:"retry_max_attempts"`
	// RetryBackoffMS 是两次重试之间的等待时间。
	RetryBackoffMS uint64 `yaml:"retry_backoff_ms"`
	// RetryableCodes 限制只有哪些 gRPC code 可以触发重试。
	RetryableCodes []string `yaml:"retryable_codes"`
}

// RuntimeConfig 聚合 agent / sidecar 两种运行模式的本地参数。
type RuntimeConfig struct {
	Agent   AgentRuntimeConfig   `yaml:"agent"`
	Sidecar SidecarRuntimeConfig `yaml:"sidecar"`
}

// AgentRuntimeConfig 描述 agent 模式的监听与容量参数。
type AgentRuntimeConfig struct {
	// Address 是 agent 对本机业务暴露的入口。
	Address string `yaml:"address"`
	// WorkerCount 预留给后续更复杂并发模型使用。
	WorkerCount int `yaml:"worker_count"`
	// MaxInflight 预留给后续限流/并发保护使用。
	MaxInflight int `yaml:"max_inflight"`
}

// SidecarRuntimeConfig 描述 sidecar 模式绑定的本地服务身份。
type SidecarRuntimeConfig struct {
	// Address 是 sidecar 的本地入口地址。
	Address string `yaml:"address"`
	// TargetMode 控制 sidecar 是否只代理上游服务，还是允许同名目标在特定范围内放行。
	TargetMode string `yaml:"target_mode"`
	// ServiceName 标识 sidecar 当前绑定的本地业务服务。
	ServiceName string `yaml:"service_name"`
	// InstanceID 标识 sidecar 当前绑定的具体实例。
	InstanceID string `yaml:"instance_id"`
	// Namespace 用于和目录/控制面下发的服务命名空间对齐。
	Namespace string `yaml:"namespace"`
	// Env 用于和目录/控制面下发的环境维度对齐。
	Env string `yaml:"env"`
}

// SourceConfig 描述目录来源选择。
type SourceConfig struct {
	// Kind 决定当前使用哪一种目录实现。
	Kind string `yaml:"kind"`
	// Consul 是 Consul 目录的连接参数。
	Consul ConsulSourceConfig `yaml:"consul"`
	// Etcd 是 etcd 目录的连接参数。
	Etcd EtcdSourceConfig `yaml:"etcd"`
}

// ConsulSourceConfig 描述 Consul 目录连接参数。
type ConsulSourceConfig struct {
	// Address 是 Consul HTTP API 地址。
	Address string `yaml:"address"`
	// Token 用于访问受保护的 Consul 集群。
	Token string `yaml:"token"`
	// Namespace 预留给和现有注册规范对齐。
	Namespace string `yaml:"namespace"`
	// Datacenter 用于跨机房部署时精确指定查询范围。
	Datacenter string `yaml:"datacenter"`
	// Scheme 默认为 http，也可以切到 https。
	Scheme string `yaml:"scheme"`
	// QueryTimeoutMS 控制单次 Consul 目录查询的最长等待时间。
	QueryTimeoutMS uint64 `yaml:"query_timeout_ms"`
	// WatchDegradeAfterErrors 控制连续 watch 查询失败多少次后把快照升级为 degraded。
	WatchDegradeAfterErrors uint32 `yaml:"watch_degrade_after_errors"`
}

// EtcdSourceConfig 描述 etcd 目录连接参数。
type EtcdSourceConfig struct {
	// Endpoints 是 etcd 集群地址列表。
	Endpoints []string `yaml:"endpoints"`
	// Username/Password 用于连接受保护的 etcd。
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// Namespace 是当前服务注册前缀。
	Namespace string `yaml:"namespace"`
	// DialTimeoutMS 控制 etcd client 建链超时。
	DialTimeoutMS uint64 `yaml:"dial_timeout_ms"`
	// QueryTimeoutMS 控制单次 etcd 目录查询的最长等待时间。
	QueryTimeoutMS uint64 `yaml:"query_timeout_ms"`
	// WatchDegradeAfterErrors 控制连续 watch 查询失败多少次后把快照升级为 degraded。
	WatchDegradeAfterErrors uint32 `yaml:"watch_degrade_after_errors"`
}

// AuthzConfig 描述 ext_authz 连接与失败策略。
type AuthzConfig struct {
	// Target 是 ext_authz gRPC 服务地址。
	Target string `yaml:"target"`
	// TimeoutMS 控制单次鉴权请求超时。
	TimeoutMS uint64 `yaml:"timeout_ms"`
	// FailOpen 决定 authz 异常时是否放行请求。
	FailOpen bool `yaml:"fail_open"`
	// IncludeHeaders 控制哪些调用 metadata 透传给 authz。
	IncludeHeaders []string `yaml:"include_headers"`
}

// ControlPlaneConfig 描述控制面连接与心跳参数。
type ControlPlaneConfig struct {
	// Enabled 控制是否启用集中控制面连接。
	Enabled bool `yaml:"enabled"`
	// Target 是控制面的 gRPC 地址。
	Target string `yaml:"target"`
	// AllowSourceFallback 控制 dataplane 在控制面没有快照时是否允许直连 source。
	AllowSourceFallback bool `yaml:"allow_source_fallback"`
	// HeartbeatIntervalMS 控制 register 后的心跳频率。
	HeartbeatIntervalMS uint64 `yaml:"heartbeat_interval_ms"`
	// ConnectTimeoutMS 控制单次连接控制面的超时。
	ConnectTimeoutMS uint64 `yaml:"connect_timeout_ms"`
}

// TelemetryConfig 描述 OTel 基础开关与导出端点。
type TelemetryConfig struct {
	// OTLPEndpoint 是 OTLP/HTTP exporter 目标地址。
	OTLPEndpoint string `yaml:"otel_endpoint"`
	// TraceEnabled 控制 trace provider 是否安装。
	TraceEnabled bool `yaml:"trace_enabled"`
	// MetricEnabled 控制 metric provider 是否安装。
	MetricEnabled bool `yaml:"metric_enabled"`
	// LogEnabled 当前只保留配置位，后续可接 log exporter。
	LogEnabled bool `yaml:"log_enabled"`
}
