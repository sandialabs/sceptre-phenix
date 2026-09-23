package v1

type RoleSpec struct {
	Name     string        `json:"roleName" mapstructure:"roleName" structs:"roleName" yaml:"roleNname"`
	Policies []*PolicySpec `json:"policies" mapstructure:"policies" structs:"policies" yaml:"policies"`
}

// PolicySpec is a single role policy. Stored configs omit empty
// ResourceNames because the schema allows the field to be missing, but not null.
type PolicySpec struct {
	Resources     []string `json:"resources"     mapstructure:"resources"     structs:"resources"               yaml:"resources"`
	ResourceNames []string `json:"resourceNames" mapstructure:"resourceNames" structs:"resourceNames,omitempty" yaml:"resourceNames"`
	Verbs         []string `json:"verbs"         mapstructure:"verbs"         structs:"verbs"                   yaml:"verbs"`
}
