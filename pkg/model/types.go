package model

// 运行模式与目录来源的字符串常量在整个仓库内共享使用，
// 这样可以避免不同层各自手写魔法字符串。
const (
	ModeAgent   = "agent"
	ModeSidecar = "sidecar"

	SourceConsul = "consul"
	SourceEtcd   = "etcd"

	SidecarTargetModeUpstreamOnly               = "upstream_only"
	SidecarTargetModeAllowSameService           = "allow_same_service"
	SidecarTargetModeAllowCrossScopeSameService = "allow_cross_scope_same_service"
)

// ServiceRef 描述一个逻辑服务目标。
type ServiceRef struct {
	// Service 是逻辑服务名，例如 orders / config。
	Service string
	// Namespace 用于区分不同注册前缀或租户空间。
	Namespace string
	// Env 用于区分 dev / test / prod 等环境维度。
	Env string
	// Port 只在部分场景下有意义，例如鉴权或目标地址补全。
	Port uint32
}

// Endpoint 描述一个可被实际命中的实例地址。
type Endpoint struct {
	// Address 是实例可访问 IP 或主机名。
	Address string
	// Port 是实例监听端口。
	Port uint32
	// Weight 为后续更复杂负载均衡策略预留。
	Weight uint32
}

// ServiceSnapshot 是目录服务解析后的统一内部结果。
type ServiceSnapshot struct {
	// Service 是这批 endpoint 对应的逻辑服务身份。
	Service ServiceRef
	// Endpoints 是当前可供选择的健康实例集合。
	Endpoints []Endpoint
	// Revision 用于标记控制面或目录快照版本。
	Revision string
}
