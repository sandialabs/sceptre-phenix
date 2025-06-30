package settings

import (
	"fmt"
	"phenix/types"
	"phenix/util/plog"
	"strconv"
	"strings"
	"unicode"
)

//Set custom hard coded limits on settings here

const (
	MAX_PASSWORD_MIN_LEN = 32
	MIN_PASSWORD_MIN_LEN = 8
)

type PasswordSettings struct {
	LowercaseReq bool  `json:"lowercase_req"`
	UppercaseReq bool  `json:"uppercase_req"`
	NumberReq    bool  `json:"number_req"`
	SymbolReq    bool  `json:"symbol_req"`
	MinLength    int32 `json:"min_length"`
}

func GetPasswordSettings() (PasswordSettings, error) {
	plog.Debug(plog.TypeSystem, "Getting all password settings")

	settings, err := List()
	if err != nil {
		return PasswordSettings{}, fmt.Errorf("error getting list: %w", err)
	}

	return GetPasswordSettingsFromList(settings)
}

func GetPasswordSettingsFromList(settings []types.Setting) (PasswordSettings, error) {
	passwordSettings := PasswordSettings{}
	var err error

	for _, setting := range settings {
		category := setting.Spec.Category
		name := setting.Spec.Name

		if category != "Password" {
			continue
		}
		switch name {
		case "NumberReq":
			passwordSettings.NumberReq, err = strconv.ParseBool(setting.Spec.Value)
			if err != nil {
				return passwordSettings, fmt.Errorf("Error parsing %s.%s setting: %w", category, name, err)
			}
		case "SymbolReq":
			passwordSettings.SymbolReq, err = strconv.ParseBool(setting.Spec.Value)
			if err != nil {
				return passwordSettings, fmt.Errorf("Error parsing %s.%s setting: %w", category, name, err)
			}
		case "LowercaseReq":
			passwordSettings.LowercaseReq, err = strconv.ParseBool(setting.Spec.Value)
			if err != nil {
				return passwordSettings, fmt.Errorf("Error parsing %s.%s setting: %w", category, name, err)
			}
		case "UppercaseReq":
			passwordSettings.UppercaseReq, err = strconv.ParseBool(setting.Spec.Value)
			if err != nil {
				return passwordSettings, fmt.Errorf("Error parsing %s.%s setting: %w", category, name, err)
			}
		case "MinLength":
			passwordSettings.MinLength, err = parseInt(setting.Spec.Value)
			if err != nil {
				return passwordSettings, fmt.Errorf("Error parsing %s.%s setting: %w", category, name, err)
			}
		}
	}
	return passwordSettings, nil
}

func UpdatePasswordSettings(newSettings PasswordSettings) error {
	plog.Debug(plog.TypeSystem, "Updating password settings")

	var err error
	_, err = Update("Password", "NumberReq", strconv.FormatBool(newSettings.NumberReq))
	if err != nil {
		return fmt.Errorf("Error updating Settings.NumberReq: %w", err)
	}
	_, err = Update("Password", "SymbolReq", strconv.FormatBool(newSettings.SymbolReq))
	if err != nil {
		return fmt.Errorf("Error updating Settings.SymbolReq: %w", err)
	}
	_, err = Update("Password", "LowercaseReq", strconv.FormatBool(newSettings.LowercaseReq))
	if err != nil {
		return fmt.Errorf("Error updating Settings.LowercaseReq: %w", err)
	}
	_, err = Update("Password", "UppercaseReq", strconv.FormatBool(newSettings.UppercaseReq))
	if err != nil {
		return fmt.Errorf("Error updating Settings.UppercaseReq: %w", err)
	}

	minLen := newSettings.MinLength
	if minLen > MAX_PASSWORD_MIN_LEN || minLen < MIN_PASSWORD_MIN_LEN {
		return fmt.Errorf("Minimum password length must be between %d and %d", MIN_PASSWORD_MIN_LEN, MAX_PASSWORD_MIN_LEN)
	}
	_, err = Update("Password", "MinLength", formatInt(newSettings.MinLength))
	if err != nil {
		return fmt.Errorf("Error updating Settings.MinLength: %w", err)
	}

	plog.Debug(plog.TypeSystem, "Updated password settings successfully")
	return nil
}

func IsPasswordValid(password string) bool {
	plog.Debug(plog.TypeSystem, "Checking if password is valid")
	ps, err := GetPasswordSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Error checking IsPasswordValid: ", "err", err)
		return false
	}
	if len(password) < int(ps.MinLength) {
		return false
	}

	var lower bool
	var upper bool
	var number bool
	var symbol bool

	for _, c := range password {
		switch {
		case unicode.IsNumber(c):
			number = true
		case unicode.IsLower(c):
			lower = true
		case unicode.IsUpper(c):
			upper = true
		case unicode.IsSymbol(c) || unicode.IsPunct(c):
			symbol = true
		}
	}

	//if req is false, want to default to true. if req is true, need var to be true
	res := (lower || !ps.LowercaseReq)
	res = res && (upper || !ps.UppercaseReq)
	res = res && (number || !ps.NumberReq)
	res = res && (symbol || !ps.SymbolReq)

	return res
}

func GetPasswordSettingsHTML() string {
	settings, err := GetPasswordSettings()
	if err != nil {
		return ""
	}

	var rules []string
	rules = append(rules, fmt.Sprintf("Password requires %d characters", settings.MinLength))

	if settings.LowercaseReq {
		rules = append(rules, "Password requires a lowercase letter")
	}
	if settings.UppercaseReq {
		rules = append(rules, "Password requires an uppercase letter")
	}
	if settings.NumberReq {
		rules = append(rules, "Password requires a number")
	}
	if settings.SymbolReq {
		rules = append(rules, "Password requires a symbol")
	}

	var sb strings.Builder

	sb.WriteString("<ol>")
	for _, rule := range rules {
		sb.WriteString("<li>")
		sb.WriteString(rule)
		sb.WriteString("</li>")
	}
	sb.WriteString("</ol>")

	return sb.String()
}
