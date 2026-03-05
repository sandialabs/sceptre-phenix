package forwardtypes

import "fmt"

type Listener struct {
	Exp string `json:"exp"`
	VM  string `json:"vm"`

	SrcPort int    `json:"srcPort"`
	DstHost string `json:"dstHost"`
	DstPort int    `json:"dstPort"`
	Owner   string `json:"owner"`

	ClusterHost string `json:"-"`
	ClusterPort int    `json:"-"`

	QEMU bool `json:"-"`
}

func (l Listener) ToKey() string {
	return fmt.Sprintf("%s:%s:%s:%d:%s", l.Exp, l.VM, l.DstHost, l.DstPort, l.Owner)
}

func (l Listener) ClusterEndpoint() string {
	return fmt.Sprintf("%s:%d", l.ClusterHost, l.ClusterPort)
}

func (l Listener) WebSocketURL(endpoint string) string {
	return fmt.Sprintf(
		"%s/api/v1/experiments/%s/vms/%s/forwards/%s/%d/ws",
		endpoint,
		l.Exp,
		l.VM,
		l.DstHost,
		l.DstPort,
	)
}
