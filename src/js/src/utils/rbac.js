import { usePhenixStore } from '@/store.js';
import { filepathMatch } from '@/utils/filepathMatch.js';

// results are cached per role object, so a new login (even with a role of
// the same name but different policies) starts with an empty cache
let cache = new Map();
let cacheRole = null;

// should match role.go#Allowed (with added caching)
export function roleAllowed(resource, verb, ...names) {
  let phenixStore = usePhenixStore();
  let role = phenixStore.role;
  if (role === null) {
    return false;
  }

  if (role !== cacheRole) {
    cache = new Map();
    cacheRole = role;
  }

  let k = JSON.stringify([resource, verb, ...names]);
  if (cache.has(k)) {
    return cache.get(k);
  }

  for (const p of role.policies) {
    for (const r of p.resources) {
      if (filepathMatch(r, resource)) {
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

// The Users page lists every user the role may list or, as users.go#GetUsers
// allows, just the signed-in user when the role may get that user.
export function usersPageAllowed() {
  return (
    roleAllowed('users', 'list') ||
    roleAllowed('users', 'get', usePhenixStore().username)
  );
}

// Creating an API token needs users patch on the user and, for another user,
// users/tokens create on them too, as auth.go#CreateUserToken checks.
export function userTokenAllowed(username) {
  if (!roleAllowed('users', 'patch', username)) {
    return false;
  }

  return (
    username === usePhenixStore().username ||
    roleAllowed('users/tokens', 'create', username)
  );
}

// should match policy.go#resourceNameAllowed
let resourceNameAllowed = (policy, name) => {
  var allowed = false;
  // a policy that names no resources can arrive with null resource names
  for (const n of policy.resourceNames ?? []) {
    let negate = n.startsWith('!');
    let n2 = negate ? n.slice(1) : n;

    if (filepathMatch(n2, name)) {
      if (negate) {
        return false;
      }
      allowed = true;
    }
  }
  return allowed;
};
