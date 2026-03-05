package v1

type RoleSpec struct {
	Name     string        `json:"roleName" mapstructure:"roleName" structs:"roleName" yaml:"roleNname"`
	Policies []*PolicySpec `json:"policies" mapstructure:"policies" structs:"policies" yaml:"policies"`
}

type PolicySpec struct {
	Resources     []string `json:"resources"     mapstructure:"resources"     structs:"resources"     yaml:"resources"`
	ResourceNames []string `json:"resourceNames" mapstructure:"resourceNames" structs:"resourceNames" yaml:"resourceNames"`
	Verbs         []string `json:"verbs"         mapstructure:"verbs"         structs:"verbs"         yaml:"verbs"`
}
