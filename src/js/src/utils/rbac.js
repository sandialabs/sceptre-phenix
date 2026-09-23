import { usePhenixStore } from '@/store.js';
import { minimatch } from 'minimatch';

// Restrict minimatch to the Go filepath.Match semantics the server uses: `*`
// never crosses a `/` namespace boundary, and minimatch-only features
// (globstars, braces, extglobs, `#` comments, `!` negation, and dotfile
// exclusion) are disabled. resourceNameAllowed handles `!` itself.
const matchOptions = {
  dot: true,
  noglobstar: true,
  nobrace: true,
  noext: true,
  nocomment: true,
  nonegate: true,
};

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
      if (minimatch(resource, r, matchOptions)) {
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
  for (const n of policy.resourceNames) {
    let negate = n.startsWith('!');
    var n2 = n.replace('!', '');

    if (minimatch(name, n2, matchOptions)) {
      if (negate) {
        return false;
      }
      allowed = true;
    }
  }
  return allowed;
};
