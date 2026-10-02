import { usePhenixStore } from '@/store.js';
import { Minimatch } from 'minimatch';

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

// results are cached per role object, so a new login (even with a role of
// the same name but different policies) starts with an empty cache
let cache = new Map();
let cacheRole = null;
// Compiled patterns, shared by every role: minimatch() compiles its pattern
// on every call, which slows the first render of a large VM table.
const matchers = new Map();
function matches(name, pattern) {
  let m = matchers.get(pattern);
  if (!m) {
    m = new Minimatch(pattern, matchOptions);
    matchers.set(pattern, m);
  }
  return m.match(name);
}

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

  let k = [resource, verb, names].join('$');
  if (cache.has(k)) {
    return cache.get(k);
  }

  for (const p of role.policies) {
    for (const r of p.resources) {
      if (matches(resource, r)) {
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
    let n2 = n.replace('!', '');

    if (matches(name, n2)) {
      if (negate) {
        return false;
      }
      allowed = true;
    }
  }
  return allowed;
};
