// The key of the page the app's router view shows: its route and path
// parameters. A link to the same route with other parameters, such as the
// jobs menu's link from one experiment to another, then mounts the page
// afresh instead of leaving the first experiment's page under the second's
// URL; a change of the query alone keeps the page.
export const viewKey = (route) =>
  `${String(route.name ?? route.path)}:${JSON.stringify(route.params ?? {})}`;
