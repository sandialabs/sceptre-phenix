// Roles for the mocked roleAllowed in mocks.js, as predicates (roles.js builds
// roles for the real rbac.js). It imports nothing, so vi.hoisted may load it.

// a role that may do anything
export const allowAll = () => true;

// a role that may do nothing
export const denyAll = () => false;

// A permission is [resource, verb, name]; a part left out, or '*', matches
// anything: ['disks'] is every verb on disks, ['*', 'delete'] any delete.
const matches = ([r, v, n], resource, verb, name) =>
  (r === undefined || r === '*' || r === resource) &&
  (v === undefined || v === '*' || v === verb) &&
  (n === undefined || n === '*' || n === name);

// a role that may do only what one of `perms` matches
export const allowOnly =
  (...perms) =>
  (resource, verb, name) =>
    perms.some((p) => matches(p, resource, verb, name));

// a role that may do anything but what one of `perms` matches
export const denyOnly =
  (...perms) =>
  (resource, verb, name) =>
    !perms.some((p) => matches(p, resource, verb, name));
