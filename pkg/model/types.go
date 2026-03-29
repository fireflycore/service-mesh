package model

// 运行模式与目录来源的字符串常量在整个仓库内共享使用，
// 这样可以避免不同层各自手写魔法字符串。
const (
	ModeAgent   = "agent"
	ModeSidecar = "sidecar"

	SourceConsul = "consul"
	SourceEtcd   = "etcd"
)

// ServiceRef 描述一个逻辑服务目标。
type ServiceRef struct {
	Service   string
	Namespace string
	Env       string
	Port      uint32
}

// Endpoint 描述一个可被实际命中的实例地址。
type Endpoint struct {
	Address string
	Port    uint32
	Weight  uint32
}

// ServiceSnapshot 是目录服务解析后的统一内部结果。
type ServiceSnapshot struct {
	Service   ServiceRef
	Endpoints []Endpoint
	Revision  string
}
