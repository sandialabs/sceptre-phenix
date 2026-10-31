// The experiment page's tabs by URL name (/experiment/demo?tab=files&q=web),
// shared by the running and stopped pages.
export const TAB = Object.freeze({
  VMS: 'vms',
  FILES: 'files',
  VNC: 'vnc',
  NETFLOW: 'netflow',
});

// The tab a URL's query names, when the page offers it, or the VMs tab.
export function tabFromQuery(query, offered) {
  const tab = query?.tab;
  return offered.includes(tab) ? tab : TAB.VMS;
}

// The search a URL's query gives.
export const searchFromQuery = (query) =>
  typeof query?.q === 'string' ? query.q : '';

// A URL's query naming a tab and its search, keeping its other keys. The VMs
// tab and an empty search are left out, so the plain URL opens them.
export function tabQuery(query, tab, search) {
  const rest = { ...query };
  delete rest.tab;
  delete rest.q;
  return {
    ...rest,
    ...(tab !== TAB.VMS && { tab }),
    ...(search && { q: search }),
  };
}

// The search a tab keeps: the VNC tab shows the VMs tab's VMs, so the two
// share one.
export const searchKey = (tab) => (tab === TAB.VNC ? TAB.VMS : tab);

// Whether two queries name the same keys and values.
export function sameQuery(a, b) {
  const keys = Object.keys(a ?? {});
  return (
    keys.length === Object.keys(b ?? {}).length &&
    keys.every((key) => String(a[key]) === String(b?.[key]))
  );
}

// The experiment page with the details of one VM open (?vm=web).
export const vmDetailsRoute = (experiment, vm) => ({
  name: 'experiment',
  params: { id: experiment },
  query: { vm },
});

// A URL's query less the VM whose details it opens.
export function withoutVm(query) {
  const rest = { ...query };
  delete rest.vm;
  return rest;
}
