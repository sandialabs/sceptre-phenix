// mirrors role_test.go
import { roleAllowed } from "./rbac";

const testRole = 
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
}

test('get any experiment', () => {
    expect(roleAllowed(testRole, "experiments", "get", "expA")).toBe(true)
    expect(roleAllowed(testRole, "experiments", "get", "expB")).toBe(true)
});

test('update only experiments/start', () => {
    expect(roleAllowed(testRole, "experiments/start", "update")).toBe(true)
    expect(roleAllowed(testRole, "experiments", "update")).toBe(false)
    expect(roleAllowed(testRole, "experiments/stop", "update")).toBe(false)
    expect(roleAllowed(testRole, "experiments/start", "update", "expA")).toBe(true)
});

test('only delete exp1', () => {
    expect(roleAllowed(testRole, "experiments", "delete", "exp1")).toBe(true)
    expect(roleAllowed(testRole, "experiments", "delete", "expB")).toBe(false)
    expect(roleAllowed(testRole, "experiments/stop", "delete", "exp1")).toBe(false)
});

test('resource single wildcard doesn\'t apply', () => {
    expect(roleAllowed(testRole, "vms", "patch", "vm1")).toBe(true)
    expect(roleAllowed(testRole, "vms/start", "patch", "vm1")).toBe(false)
})

test('resource name restriction', () => {
    expect(roleAllowed(testRole, "vms", "patch", "vm1")).toBe(true)
    expect(roleAllowed(testRole, "vms", "patch", "vmB")).toBe(false)
    expect(roleAllowed(testRole, "experiments", "patch", "expA")).toBe(false)
})

test('resourceName single wildcard DOES apply', ()=> {
    expect(roleAllowed(testRole, "vms", "delete", "vm1")).toBe(true)
    expect(roleAllowed(testRole, "vms", "delete", "expA/vm1")).toBe(true)
})

test('resourceName negation', () => {
    expect(roleAllowed(testRole, "things", "delete", "thing")).toBe(true)
    expect(roleAllowed(testRole, "things", "delete", "thing1")).toBe(false)
    expect(roleAllowed(testRole, "things", "delete", "thing2")).toBe(true)
})

test('resourceName mid-wildcard', () => {
    expect(roleAllowed(testRole, "items", "delete", "item")).toBe(true)
    expect(roleAllowed(testRole, "items", "delete", "item1")).toBe(true)
    expect(roleAllowed(testRole, "items", "delete", "thing")).toBe(false)
})