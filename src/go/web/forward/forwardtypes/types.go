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
}

func (this Listener) ToKey() string {
	return fmt.Sprintf("%s:%s:%s:%d:%s", this.Exp, this.VM, this.DstHost, this.DstPort, this.Owner)
}

func (this Listener) ClusterEndpoint() string {
	return fmt.Sprintf("%s:%d", this.ClusterHost, this.ClusterPort)
}

func (this Listener) WebSocketURL(endpoint string) string {
	return fmt.Sprintf("%s/api/v1/experiments/%s/vms/%s/forwards/%s/%d/ws", endpoint, this.Exp, this.VM, this.DstHost, this.DstPort)
}
