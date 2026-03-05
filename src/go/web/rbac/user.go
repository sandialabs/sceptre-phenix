package rbac

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"
	"golang.org/x/crypto/bcrypt"

	"phenix/api/config"
	"phenix/store"
	v1 "phenix/types/version/v1"
)

/*
version: v1
kind: User
metadata:
	name: <username>
spec:
	username: <username>
	password: <bas64 encoded password>
	firstName: <first name>
	lastName: <last name>
	rbac:
		roleName: <role name>
		policies:
		- resources:
			- vms
			resourceNames:
			- foo_*
			- bar_inverter
			verbs:
			- list
		- resources:
			- vms/screenshot
			- vms/vnc
			resourceNames:
			- foo_*
			- bar_inverter
			verbs:
			- get
*/

var ErrPasswordInvalid = errors.New("password invalid")

const tokenProxied = "proxied"

type User struct {
	Spec *v1.UserSpec

	config *store.Config
}

func NewUser(u, p string) *User {
	hashed, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		return nil
	}

	spec := &v1.UserSpec{ //nolint:exhaustruct // partial initialization
		Username: u,
		Password: string(hashed),
	}

	c := &store.Config{ //nolint:exhaustruct // partial initialization
		Version:  "phenix.sandia.gov/v1",
		Kind:     "User",
		Metadata: store.ConfigMetadata{Name: u}, //nolint:exhaustruct // partial initialization
		Spec:     structs.MapDefaultCase(spec, structs.CASESNAKE),
	}

	if err := store.Create(c); err != nil {
		return nil
	}

	return &User{Spec: spec, config: c}
}

func GetUsers() ([]*User, error) {
	configs, err := config.List("user")
	if err != nil {
		return nil, fmt.Errorf("getting user configs: %w", err)
	}

	users := make([]*User, len(configs))

	for i, c := range configs {
		var u v1.UserSpec

		err := mapstructure.Decode(c.Spec, &u)
		if err != nil {
			return nil, fmt.Errorf("decoding user config: %w", err)
		}

		users[i] = &User{Spec: &u, config: &c}
	}

	return users, nil
}

func GetUser(uname string) (*User, error) {
	c, err := config.Get("user/"+uname, false)
	if err != nil {
		return nil, fmt.Errorf("getting user config: %w", err)
	}

	var u v1.UserSpec
	if err := mapstructure.Decode(c.Spec, &u); err != nil {
		return nil, fmt.Errorf("decoding user config: %w", err)
	}

	return &User{Spec: &u, config: c}, nil
}

func (u User) Username() string {
	return u.Spec.Username
}

func (u User) FirstName() string {
	return u.Spec.FirstName
}

func (u User) LastName() string {
	return u.Spec.LastName
}

func (u User) RoleName() string {
	if u.Spec.Role == nil {
		disabled, err := RoleFromConfig("disabled")
		if err != nil {
			return ""
		}

		return disabled.Spec.Name
	}

	return u.Spec.Role.Name
}

func (u User) UpdateFirstName(name string) error {
	u.Spec.FirstName = name

	u.config.Spec = structs.MapDefaultCase(u.Spec, structs.CASESNAKE)

	err := u.Save()
	if err != nil {
		return fmt.Errorf("updating user first name: %w", err)
	}

	return nil
}

func (u User) UpdateLastName(name string) error {
	u.Spec.LastName = name

	u.config.Spec = structs.MapDefaultCase(u.Spec, structs.CASESNAKE)

	err := u.Save()
	if err != nil {
		return fmt.Errorf("updating user last name: %w", err)
	}

	return nil
}

func (u User) UpdatePassword(old, newPass string) error {
	if err := u.ValidatePassword(old); err != nil {
		return err
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("generating password hash: %w", err)
	}

	u.Spec.Password = string(hashed)
	u.config.Spec = structs.MapDefaultCase(u.Spec, structs.CASESNAKE)

	if err := u.Save(); err != nil {
		return fmt.Errorf("updating user password: %w", err)
	}

	return nil
}

func (u User) GetProxyToken() string {
	for token, note := range u.Spec.Tokens {
		if note == tokenProxied {
			return token
		}
	}

	return ""
}

func (u User) AddToken(token, note string) error {
	if u.Spec.Tokens == nil {
		u.Spec.Tokens = make(map[string]string)
	}

	if note == tokenProxied {
		// we only want to keep one proxy JWT
		for k, v := range u.Spec.Tokens {
			if v == tokenProxied {
				delete(u.Spec.Tokens, k)
			}
		}
	}

	enc := base64.StdEncoding.EncodeToString([]byte(token))

	u.Spec.Tokens[enc] = note
	u.config.Spec = structs.MapDefaultCase(u.Spec, structs.CASESNAKE)

	err := u.Save()
	if err != nil {
		return fmt.Errorf("persisting new user token: %w", err)
	}

	return nil
}

func (u User) DeleteToken(token string) error {
	enc := base64.StdEncoding.EncodeToString([]byte(token))

	delete(u.Spec.Tokens, enc)

	u.config.Spec = structs.MapDefaultCase(u.Spec, structs.CASESNAKE)

	err := u.Save()
	if err != nil {
		return fmt.Errorf("deleting user token: %w", err)
	}

	return nil
}

func (u User) ValidateToken(token string) error {
	enc := base64.StdEncoding.EncodeToString([]byte(token))

	if _, ok := u.Spec.Tokens[enc]; ok {
		return nil
	}

	return errors.New("token not found for user")
}

func (u User) ValidatePassword(p string) error {
	err := bcrypt.CompareHashAndPassword([]byte(u.Spec.Password), []byte(p))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return ErrPasswordInvalid
		}

		return err
	}

	return nil
}

func (u User) Save() error {
	err := store.Update(u.config)
	if err != nil {
		return fmt.Errorf("updating user in store: %w", err)
	}

	return nil
}

func (u User) Role() (Role, error) {
	if u.Spec.Role == nil {
		disabled, err := RoleFromConfig("disabled")
		if err != nil {
			return Role{}, fmt.Errorf("getting disabled role: %w", err)
		}

		return *disabled, nil
	}

	return Role{Spec: u.Spec.Role}, nil //nolint:exhaustruct // partial initialization
}

func (u *User) SetRole(role *Role) error {
	u.Spec.Role = role.Spec
	u.config.Spec = structs.MapDefaultCase(u.Spec, structs.CASESNAKE)

	err := u.Save()
	if err != nil {
		return fmt.Errorf("setting user role: %w", err)
	}

	return nil
}
