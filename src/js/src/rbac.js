var minimatch = require("minimatch");

let cache = new Map();
// should match role.go#Allowed (with added caching)
let roleAllowed = (role, resource, verb, ...names) => {
    if (role === null) {
      return false
    }
    // remove any undefined or empty names
    names = names.filter((n) => n)

    let k = [role.name, resource, verb, names].join("$")
    if (cache.has(k)) {
      return cache.get(k)
    }

    for (const p of role.policies) {  
      for (const r of p.resources) {  
        if (minimatch(resource, r)) {
          for (const v of p.verbs) {
            if (v == "*" || v == verb) {
              if (names.length == 0) {
                cache.set(k, true)
                return true
              }
              for (const name of names) {
                if (name && resourceNameAllowed(p, name)) {
                  cache.set(k, true)
                  return true
                }
              }
            }
          }
        }
      }
    }
    cache.set(k, false)
    return false
  }

// should match policy.go#resourceNameAllowed
let resourceNameAllowed = (policy, name) => {
    var allowed = false
    for (const n of policy.resourceNames) {
      let negate = n.startsWith("!")
      var n2 = n.replace("!", "")
      
      if (name.includes("/") && !n2.includes("/")) {
        n2 = "*/" + n2
      }

      if (minimatch(name, n2)) {
        if (negate) {
          return false
        }
        allowed = true
      }
    }
    return allowed
}

export {roleAllowed};