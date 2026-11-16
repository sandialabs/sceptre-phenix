package v1

type UserSpec struct {
	Username  string    `json:"username"   mapstructure:"username"   structs:"username"   yaml:"username"`
	Password  string    `json:"password"   mapstructure:"password"   structs:"password"   yaml:"password"` //nolint:gosec // G117 a stored user's password field
	FirstName string    `json:"first_name" mapstructure:"first_name" structs:"first_name" yaml:"firstName"`
	LastName  string    `json:"last_name"  mapstructure:"last_name"  structs:"last_name"  yaml:"lastName"`
	Role      *RoleSpec `json:"rbac"       mapstructure:"rbac"       structs:"rbac"       yaml:"rbac"`

	Tokens map[string]string `json:"tokens" mapstructure:"tokens" structs:"tokens" yaml:"tokens"`
}
