/** A page's title (WCAG 2.4.2): the most specific part first, then the app. */
export function pageTitle(...parts) {
  return [...parts.filter(Boolean), 'phēnix'].join(' - ');
}

/**
 * The title of a route: the item it shows (WCAG 2.4.8), the page name from
 * its `meta.title`, then the app. `meta.titleParam` names the route param
 * that holds the item, such as the experiment name.
 */
export function routeTitle(route) {
  const param = route.meta?.titleParam;
  let item = param ? route.params?.[param] : null;

  if (Array.isArray(item)) {
    item = item.join('/');
  }

  return pageTitle(item, route.meta?.title);
}
