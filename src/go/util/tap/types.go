package tap

type Tap struct {
	Bridge   string   `structs:"bridge" mapstructure:"bridge"`
	VLAN     string   `structs:"vlan" mapstructure:"vlan"`
	IP       string   `structs:"ip" mapstructure:"ip"`
	External External `structs:"externalAccess" mapstructure:"externalAccess"`

	Name   string `structs:"name" mapstructure:"name"`
	Subnet string `structs:"subnet" mapstructure:"subnet"`

	// Using this to provide backwards compatibility with the original Scorch
	// tap/break component, where it's using `internetAccess` instead of
	// `externalAccess`.
	Other map[string]interface{} `structs:"-" mapstructure:",remain"`

	o options
}

type External struct {
	Enabled bool `structs:"enabled" mapstructure:"enabled"`
	// TODO: add firewall config
}
