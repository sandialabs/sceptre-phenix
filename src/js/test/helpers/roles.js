// A role allowed each [resource, verb] pair, on every resource name unless a
// pair lists the names: role('Viewer', ['vms', 'list'], ['vms', 'get', ['web']]).
export const role = (name, ...pairs) => ({
  name,
  policies: pairs.map(([resource, verb, names = ['*']]) => ({
    resources: [resource],
    resourceNames: names,
    verbs: [verb],
  })),
});
