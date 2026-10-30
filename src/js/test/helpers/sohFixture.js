// A small State of Health graph in the /soh response shape: a firewall and
// a router, four VLANs, single-homed hosts, one multi-homed host.
export function sohFixture() {
  const nodes = [
    {
      id: 0,
      label: 'fw',
      image: 'Firewall',
      tags: {},
      status: 'running',
      soh: null,
    },
    {
      id: 1,
      label: 'rtr',
      image: 'Router',
      tags: {},
      status: 'running',
      soh: null,
    },
  ];
  const edges = [];
  const vlans = ['internet', 'dmz', 'corp', 'ot'];
  let id = 100;
  const vlanIds = {};
  for (const v of vlans) {
    vlanIds[v] = id;
    nodes.push({
      id: id++,
      label: v,
      image: 'switch',
      tags: {},
      status: 'ignore',
      soh: null,
    });
  }
  const link = (s, t) =>
    edges.push({ id: edges.length, source: s, target: t, length: 150 });
  link(0, vlanIds.internet);
  link(0, vlanIds.dmz);
  link(1, vlanIds.dmz);
  link(1, vlanIds.corp);
  link(1, vlanIds.ot);
  const statuses = [
    'running',
    'notrunning',
    'notboot',
    'notdeploy',
    'external',
  ];
  let h = 2;
  for (const [v, n] of [
    ['dmz', 3],
    ['corp', 9],
    ['ot', 4],
  ]) {
    for (let i = 0; i < n; i++) {
      nodes.push({
        id: h,
        label: `${v}-${i}`,
        image: i % 2 ? 'windows' : 'linux',
        tags: {},
        status: statuses[h % statuses.length],
        soh: null,
      });
      link(h, vlanIds[v]);
      h++;
    }
  }
  // multi-homed historian between corp and ot
  nodes.push({
    id: h,
    label: 'historian',
    image: 'centos',
    tags: {},
    status: 'running',
    soh: null,
  });
  link(h, vlanIds.corp);
  link(h, vlanIds.ot);
  return { nodes, edges };
}
