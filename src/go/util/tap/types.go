package tap

type Tap struct {
	Bridge   string   `mapstructure:"bridge"         structs:"bridge"`
	VLAN     string   `mapstructure:"vlan"           structs:"vlan"`
	IP       string   `mapstructure:"ip"             structs:"ip"`
	External External `mapstructure:"externalAccess" structs:"externalAccess"`

	Name   string `mapstructure:"name"   structs:"name"`
	Subnet string `mapstructure:"subnet" structs:"subnet"`

	// Using this to provide backwards compatibility with the original Scorch
	// tap/break component, where it's using `internetAccess` instead of
	// `externalAccess`.
	Other map[string]any `mapstructure:",remain" structs:"-"`

	o options
}

type External struct {
	Enabled  bool      `mapstructure:"enabled"  structs:"enabled"`
	Firewall *Firewall `mapstructure:"firewall" structs:"firewall"`
}

type Firewall struct {
	// Default is the action taken when no rule matches a packet. Valid values
	// are "accept", "drop", and "reject". Defaults to "drop" when unset.
	Default string          `mapstructure:"default" structs:"default"`
	Rules   []*FirewallRule `mapstructure:"rules"   structs:"rules"`
}

// FirewallRule matches packets leaving the experiment VLAN through the tap for
// external networks. Rules are evaluated in order and the first match wins.
// Omitted match criteria mean "any".
type FirewallRule struct {
	// Action is one of "accept", "drop", or "reject".
	Action string `mapstructure:"action" structs:"action"`
	// Description is informational only and is never included in generated
	// firewall commands.
	Description string `mapstructure:"description" structs:"description"`

	Source      *FirewallEndpoint `mapstructure:"source"      structs:"source"`
	Destination *FirewallEndpoint `mapstructure:"destination" structs:"destination"`

	// Protocol/Protocols accept "tcp", "udp", "icmp", and "all". Both fields
	// are merged when validated; "all" (the default) cannot be combined with
	// other protocols, and ports require tcp and/or udp.
	Protocol  string   `mapstructure:"protocol"  structs:"protocol"`
	Protocols []string `mapstructure:"protocols" structs:"protocols"`
}

// FirewallEndpoint describes one side of a connection. The singular and plural
// fields are merged when validated. Addresses can be bare IPs or CIDRs.
type FirewallEndpoint struct {
	Address   string   `mapstructure:"address"   structs:"address"`
	Addresses []string `mapstructure:"addresses" structs:"addresses"`
	Port      int      `mapstructure:"port"      structs:"port"`
	Ports     []int    `mapstructure:"ports"     structs:"ports"`
}
