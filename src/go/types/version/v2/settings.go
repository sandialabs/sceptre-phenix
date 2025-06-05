package v2

type SettingValueType string

const (
	SettingValueInt    SettingValueType = "int"
	SettingValueFloat  SettingValueType = "float64"
	SettingValueBool   SettingValueType = "bool"
	SettingValueString SettingValueType = "string"
)

type Setting struct {
	Category string           `json:"category" yaml:"category"`
	Name     string           `json:"name" yaml:"name"`
	Value    string           `json:"value" yaml:"value"`
	Type     SettingValueType `json:"type" yaml:"type"`
}
