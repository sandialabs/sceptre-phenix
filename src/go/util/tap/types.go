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
	Enabled bool `mapstructure:"enabled" structs:"enabled"`
}
