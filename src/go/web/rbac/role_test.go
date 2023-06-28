// mirrors rbac.test.js
package rbac

import (
	"encoding/json"
	"fmt"
	"os"
	v1 "phenix/types/version/v1"
	"testing"
)

const roleJson = `
{
    "name": "Test Role",
    "policies": [
        {
            "resources": ["experiments"],
            "resourceNames": ["*", "*/*"],
            "verbs": ["get"]
        },
        {
            "resources": ["experiments/start"],
            "resourceNames": ["*", "*/*"],
            "verbs": ["update"] 
        },
        {
            "resources": ["experiments"],
            "resourceNames": ["exp1"],
            "verbs": ["delete"] 
        },
        {
            "resources": ["*"],
            "resourceNames": ["vm1"],
            "verbs": ["patch"]
        },
        {
            "resources": ["vms"],
            "resourceNames": ["*"],
            "verbs": ["delete"]
        },
        {
            "resources": ["things"],
            "resourceNames": ["*", "!thing1"],
            "verbs": ["*"]
        },
        {
            "resources": ["items"],
            "resourceNames": ["item*"],
            "verbs": ["*"]
        }
    ]
}`

var role Role

// load json into role struct
func setup() {
	var spec = &v1.RoleSpec{}
	err := json.Unmarshal([]byte(roleJson), spec)
    fmt.Println(err)
	role = Role{Spec: spec}
}

// compare result and expected. Report error if they don't match
func expect(result, expected bool, t *testing.T) {
    t.Helper()
    meaning := map[bool]string{ true: "allowed", false: "disallowed", }
    if result != expected {
        t.Errorf("Unexpected value. Expected %s got %s", meaning[expected], meaning[result])
    }
}

func TestGetAnyExperiment(t *testing.T) {
	expect(role.Allowed("experiments", "get", "expA"), true, t)
	expect(role.Allowed("experiments", "get", "expB"), true, t)
}

func TestUpdateExperimentStart(t *testing.T) {
	expect(role.Allowed("experiments/start", "update"), true, t)
	expect(role.Allowed("experiments", "update"), false, t)
    expect(role.Allowed("experiments/stop", "update"), false, t)
    expect(role.Allowed("experiments/start", "update", "expA"), true, t)
}

func TestOnlyDeleteExp1(t *testing.T) {
    expect(role.Allowed("experiments", "delete", "exp1"), true, t)
    expect(role.Allowed("experiments", "delete", "expB"), false, t)
    expect(role.Allowed("experiments/stop", "delete", "exp1"), false, t)
}

func TestResourceSingleWildcard(t *testing.T) {
    expect(role.Allowed("vms", "patch", "vm1"), true, t)
    expect(role.Allowed("vms/start", "patch", "vm1"), false, t)
}

func TestResourceNameRestriction(t *testing.T) {
    expect(role.Allowed("vms", "patch", "vm1"), true, t)
    expect(role.Allowed("vms", "patch", "vmB"), false, t)
    expect(role.Allowed("experiments", "patch", "expA"), false, t)
}

func TestResourceNameSingleWildcardApplies(t *testing.T) {
    expect(role.Allowed("vms", "delete", "vm1"), true, t)
    expect(role.Allowed("vms", "delete", "expA/vm1"), true, t)
}

func TestResourceNameNegation(t *testing.T) {
    expect(role.Allowed("things", "delete", "thing"), true, t)
    expect(role.Allowed("things", "delete", "thing1"), false, t)
    expect(role.Allowed("things", "delete", "thing2"), true, t)
}

func TestResourceNameMidWildcard(t *testing.T) {
    expect(role.Allowed("items", "delete", "item"), true, t)
    expect(role.Allowed("items", "delete", "item1"), true, t)
    expect(role.Allowed("items", "delete", "thing"), false, t)
}

func TestMain(m *testing.M) {
	setup()
	os.Exit(m.Run())
}