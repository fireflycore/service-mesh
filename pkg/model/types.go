package model

const (
	ModeAgent   = "agent"
	ModeSidecar = "sidecar"

	SourceConsul = "consul"
	SourceEtcd   = "etcd"
)

type ServiceRef struct {
	Service   string
	Namespace string
	Env       string
	Port      uint32
}

type Endpoint struct {
	Address string
	Port    uint32
	Weight  uint32
}

type ServiceSnapshot struct {
	Service   ServiceRef
	Endpoints []Endpoint
	Revision  string
}
