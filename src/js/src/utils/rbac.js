import { usePhenixStore } from '@/store.js';
import { minimatch } from 'minimatch';

let cache = new Map();
// should match role.go#Allowed (with added caching)
export function roleAllowed(resource, verb, ...names) {
  let phenixStore = usePhenixStore();
  let role = phenixStore.role;
  if (role === null) {
    return false;
  }

  let k = [role.name, resource, verb, names].join('$');
  if (cache.has(k)) {
    return cache.get(k);
  }

  for (const p of role.policies) {
    for (const r of p.resources) {
      if (minimatch(resource, r)) {
        for (const v of p.verbs) {
          if (v == '*' || v == verb) {
            if (names.length == 0) {
              cache.set(k, true);
              return true;
            }
            for (const name of names) {
              if (name && resourceNameAllowed(p, name)) {
                cache.set(k, true);
                return true;
              }
            }
          }
        }
      }
    }
  }
  cache.set(k, false);
  return false;
}

// should match policy.go#resourceNameAllowed
let resourceNameAllowed = (policy, name) => {
  var allowed = false;
  // unscoped policies arrive with null resource names and match no name
  for (const n of policy.resourceNames ?? []) {
    let negate = n.startsWith('!');
    var n2 = n.replace('!', '');

    if (name.includes('/') && !n2.includes('/')) {
      n2 = '*/' + n2;
    }

    if (minimatch(name, n2)) {
      if (negate) {
        return false;
      }
      allowed = true;
    }
  }
  return allowed;
};

// Starting a Scorch run needs scorch post and canceling one needs scorch
// delete, both for an experiment the user can read, matching the server's
// Scorch pipeline routes.
export function scorchControlAllowed(exp, running) {
  return (
    roleAllowed('scorch', running ? 'delete' : 'post') &&
    roleAllowed('experiments', 'get', exp)
  );
}
