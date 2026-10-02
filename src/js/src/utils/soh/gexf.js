// GEXF 1.3 export of the State of Health graph, for Gephi and Gephi Lite.
// Built in the browser from the experiment's state of health (GET
// /experiments/{name}/soh) already on the page, plus the experiment's VM list
// when the user may read it (host, addresses, CPUs, memory, disk). See
// https://gexf.net and the 1.3 schema at
// https://github.com/gephi/gexf/tree/master/specs/1.3.
//
// Visual data (color, position, size, shape) uses the viz namespace
// http://gexf.net/1.3/viz, as Gephi, graphology (Gephi Lite) and NetworkX
// write and read it.
import {
  STATUS_LABELS,
  displayColor,
  isInfra,
  isVlan,
  SOH_STYLE_LABEL_KEY,
} from './graph.js';
import { escapeXml, xmlAttrs } from './xml.js';

const GEXF_NS = 'http://gexf.net/1.3';
const GEXF_VIZ_NS = 'http://gexf.net/1.3/viz';

// The SOH check categories of a host, with what identifies one check in its
// metadata.
const SOH_CATEGORIES = [
  ['networking', 'networking', []],
  ['reachability', 'reachability', ['target']],
  ['processes', 'processes', ['proc']],
  ['listeners', 'listeners', ['port']],
  ['services', 'services', ['service']],
  ['files', 'files', ['path']],
  ['filesAbsent', 'files_absent', ['path']],
  ['containers', 'containers', ['container']],
  ['customTests', 'custom_tests', ['test']],
];

const NODE_ATTRIBUTES = [
  ['node_type', 'string'], // vm, router, firewall, vlan or address
  ['os_type', 'string'],
  ['status', 'string'],
  ['status_label', 'string'],
  ['vm_state', 'string'],
  ['host', 'string'],
  ['ip_addresses', 'liststring'],
  ['vlans', 'liststring'],
  ['interface_count', 'integer'],
  ['cpus', 'integer'],
  ['memory_mb', 'integer'],
  ['disk_image', 'string'],
  ['uptime_s', 'double'],
  ['external', 'boolean'],
  ['do_not_boot', 'boolean'],
  ['description', 'string'],
  ['tags', 'liststring'],
  ['notes', 'string'],
  ['custom_style', 'string'],
  ['soh_available', 'boolean'],
  ['soh_errors', 'boolean'],
  ['cpu_load', 'string'],
  ['cpu_load_1m', 'double'],
  ['soh_last_check', 'string'],
  ...SOH_CATEGORIES.flatMap(([, name]) => [
    [`soh_${name}_ok`, 'integer'],
    [`soh_${name}_failed`, 'integer'],
  ]),
  ['soh_failed_total', 'integer'],
  ['soh_failures', 'string'],
];

const EDGE_ATTRIBUTES = [
  ['relation', 'string'], // interface or flow
  ['vlan', 'string'],
  ['interface_index', 'integer'],
  ['ip_address', 'string'],
  ['bytes', 'long'],
];

const MAX_FAILURE_TEXT = 160; // per failed check
const MAX_FAILURES_TEXT = 2000; // per host

// A GEXF 1.3 list value: [a, b], with items quoted when they would
// otherwise be ambiguous.
function gexfList(items) {
  const quote = (v) => {
    const s = String(v);
    return /^[^\s,[\]"'\\]+(?:\s+[^\s,[\]"'\\]+)*$/.test(s)
      ? s
      : JSON.stringify(s);
  };
  return `[${items.map(quote).join(', ')}]`;
}

// minimega lists a VM's networks as "NAME (VLAN id)" or "NAME"
const networkName = (n) => String(n).replace(/ \(\d*\)$/, '');

function attvalue(id, type, value) {
  if (value === undefined || value === null) return '';
  if (typeof value === 'number' && !Number.isFinite(value)) return '';
  if (Array.isArray(value)) {
    if (!value.length) return '';
    value = gexfList(value);
  } else if (type === 'integer' || type === 'long') {
    value = Math.round(Number(value));
    if (!Number.isFinite(value)) return '';
  }
  return `<attvalue${xmlAttrs({ for: id, value: String(value) })}/>`;
}

function truncate(text, max) {
  const s = String(text ?? '')
    .replace(/\s+/g, ' ')
    .trim();
  return s.length > max ? s.slice(0, max - 1) + '…' : s;
}

// Counts of passed and failed checks per category, the latest check time
// and a readable list of what failed.
function summarizeSoh(soh) {
  const out = { counts: {}, failed: 0, failures: [], last: '' };
  for (const [key, name, idKeys] of SOH_CATEGORIES) {
    const states = Array.isArray(soh?.[key]) ? soh[key] : [];
    let ok = 0;
    let bad = 0;
    for (const st of states) {
      if (st?.timestamp && String(st.timestamp) > out.last) {
        out.last = String(st.timestamp);
      }
      if (st?.error) {
        bad++;
        const what = idKeys
          .map((k) => st.metadata?.[k])
          .filter((v) => v !== undefined && v !== null && v !== '')
          .join(' ');
        out.failures.push(
          `${name.replace(/_/g, ' ')}${what ? ' ' + what : ''}: ${truncate(st.error, MAX_FAILURE_TEXT)}`,
        );
      } else {
        ok++;
      }
    }
    out.counts[name] = { ok, failed: bad };
    out.failed += bad;
  }
  return out;
}

function failureText(failures) {
  let text = '';
  for (let i = 0; i < failures.length; i++) {
    const next = (text ? text + '; ' : '') + failures[i];
    if (next.length > MAX_FAILURES_TEXT) {
      return `${text}; …and ${failures.length - i} more`;
    }
    text = next;
  }
  return text;
}

function nodeType(n) {
  if (isVlan(n)) return 'vlan';
  if (isInfra(n)) return String(n.image).toLowerCase();
  return 'vm';
}

const NODE_SIZE = { vm: 10, router: 14, firewall: 14, vlan: 8, address: 6 };
const NODE_SHAPE = {
  vm: 'disc',
  router: 'diamond',
  firewall: 'diamond',
  vlan: 'square',
  address: 'triangle',
};

function hexToRgb(hex) {
  const v = parseInt(hex.slice(1), 16);
  return { r: (v >> 16) & 255, g: (v >> 8) & 255, b: v & 255 };
}

function vizColor(hex) {
  return `<viz:color${xmlAttrs({ ...hexToRgb(hex), hex })}/>`;
}

const round = (v) => Math.round(v * 100) / 100;

// Builds the GEXF document. positions maps node id to the {x, y} the graph
// shows; y is negated because Gephi's y axis points up. vms is the
// experiment's VM list (optional). hosts and flows are the state of health's
// hosts and host_flows: flows[i][j] is the bytes hosts[i] sent to hosts[j].
export function buildGexf({
  experiment,
  nodes = [],
  edges = [],
  positions = new Map(),
  running = false,
  vms = null,
  hosts = null,
  flows = null,
  layout = '',
  filter = '',
  now = new Date(),
}) {
  const vmByName = new Map((vms ?? []).map((v) => [v.name, v]));
  const byId = new Map(nodes.map((n) => [n.id, n]));

  // each VM's VLANs, in interface order when the VM list says it
  const vmEdges = new Map();
  for (const e of edges) {
    if (!byId.has(e.source) || !byId.has(e.target)) continue;
    if (!vmEdges.has(e.source)) vmEdges.set(e.source, []);
    vmEdges.get(e.source).push(e);
  }
  const edgeInfo = new Map();
  for (const [id, list] of vmEdges) {
    const vm = vmByName.get(byId.get(id).label);
    const nets = (vm?.networks ?? []).map(networkName);
    list.forEach((e, i) => {
      const vlan = byId.get(e.target)?.label;
      const idx = nets.length ? nets.indexOf(vlan) : i;
      edgeInfo.set(e.id, {
        vlan,
        index: idx >= 0 ? idx : undefined,
        ip: idx >= 0 ? vm?.ipv4?.[idx] || undefined : undefined,
      });
    });
  }

  // flow endpoints are IP addresses: map them to VMs, or add them as
  // address nodes when no VM has them
  const nodeByIp = new Map();
  for (const n of nodes) {
    for (const ip of vmByName.get(n.label)?.ipv4 ?? []) {
      if (ip && !nodeByIp.has(ip)) nodeByIp.set(ip, n.id);
    }
    if (!nodeByIp.has(n.label)) nodeByIp.set(n.label, n.id);
  }
  const addressNodes = new Map();
  const flowEdges = [];
  if (Array.isArray(hosts) && Array.isArray(flows)) {
    const endpoint = (name) => {
      if (nodeByIp.has(name)) return nodeByIp.get(name);
      if (!addressNodes.has(name)) addressNodes.set(name, `ip:${name}`);
      return addressNodes.get(name);
    };
    hosts.forEach((src, i) => {
      (flows[i] ?? []).forEach((bytes, j) => {
        if (i === j || !(Number(bytes) > 0) || hosts[j] === undefined) return;
        flowEdges.push({
          source: endpoint(src),
          target: endpoint(hosts[j]),
          bytes: Number(bytes),
        });
      });
    });
  }

  const out = [];
  const vmCount = nodes.filter((n) => !isVlan(n)).length;
  const vlanCount = nodes.length - vmCount;
  const description =
    `State of health topology of phenix experiment ${experiment}: ` +
    `${vmCount} VMs and ${vlanCount} VLANs` +
    (filter ? `, filter: ${filter}` : '') +
    (layout ? `, layout: ${layout}` : '') +
    `, experiment ${running ? 'running' : 'not running'}` +
    (vms
      ? ''
      : ', VM details (host, addresses, CPUs, memory, disk) not available') +
    `. Exported ${now.toISOString()}.`;

  out.push('<?xml version="1.0" encoding="UTF-8"?>');
  out.push(
    `<gexf xmlns="${GEXF_NS}" xmlns:viz="${GEXF_VIZ_NS}" ` +
      'xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" ' +
      `xsi:schemaLocation="${GEXF_NS} ${GEXF_NS}/gexf.xsd" version="1.3">`,
  );
  out.push(`  <meta lastmodifieddate="${now.toISOString().slice(0, 10)}">`);
  out.push('    <creator>phenix</creator>');
  out.push(`    <description>${escapeXml(description)}</description>`);
  out.push(
    `    <keywords>${escapeXml(`phenix, state of health, ${experiment}`)}</keywords>`,
  );
  out.push('  </meta>');
  out.push('  <graph defaultedgetype="undirected" mode="static">');

  const declare = (cls, attrs) => {
    out.push(`    <attributes class="${cls}">`);
    for (const [id, type] of attrs) {
      out.push(`      <attribute id="${id}" title="${id}" type="${type}"/>`);
    }
    out.push('    </attributes>');
  };
  declare('node', NODE_ATTRIBUTES);
  declare('edge', EDGE_ATTRIBUTES);
  const nodeType_ = Object.fromEntries(NODE_ATTRIBUTES);
  const edgeType = Object.fromEntries(EDGE_ATTRIBUTES);

  out.push(`    <nodes count="${nodes.length + addressNodes.size}">`);
  for (const n of nodes) {
    const type = nodeType(n);
    const vm = type === 'vlan' ? undefined : vmByName.get(n.label);
    const values = {
      node_type: type,
      status: n.status,
    };
    if (type !== 'vlan') {
      const tags = Object.entries(n.tags ?? {}).filter(
        ([k]) => !k.startsWith('__'),
      );
      const notes = Object.entries(n.tags ?? {})
        .filter(([k]) => k.startsWith('__notes_'))
        .map(([, v]) => v);
      const nets = vm?.networks?.length
        ? vm.networks.map(networkName)
        : (vmEdges.get(n.id) ?? []).map((e) => byId.get(e.target)?.label);
      Object.assign(values, {
        os_type: type === 'vm' ? String(n.image).toLowerCase() : undefined,
        status_label: STATUS_LABELS[n.status] ?? n.status,
        vm_state: vm?.state || undefined,
        host: vm?.host || undefined,
        ip_addresses: (vm?.ipv4 ?? []).filter(Boolean),
        vlans: nets.filter(Boolean),
        interface_count: nets.length,
        cpus: vm?.cpus || undefined,
        memory_mb: vm?.ram || undefined,
        disk_image: vm?.disk || undefined,
        uptime_s: vm?.running ? vm.uptime : undefined,
        external: vm ? Boolean(vm.external) : n.status === 'external',
        do_not_boot: vm ? Boolean(vm.dnb) : n.status === 'notboot',
        description: vm?.description || undefined,
        tags: tags.map(([k, v]) => `${k}=${v}`),
        notes: notes.length ? notes.join('\n') : undefined,
        custom_style: n.tags?.[SOH_STYLE_LABEL_KEY] || undefined,
        soh_available: Boolean(n.soh),
      });
      if (n.soh) {
        const s = summarizeSoh(n.soh);
        values.soh_errors = Boolean(n.soh.errors) || s.failed > 0;
        values.cpu_load = n.soh.cpuLoad || undefined;
        const load = parseFloat(n.soh.cpuLoad);
        values.cpu_load_1m = Number.isFinite(load) ? load : undefined;
        values.soh_last_check = s.last || undefined;
        for (const [name, c] of Object.entries(s.counts)) {
          values[`soh_${name}_ok`] = c.ok;
          values[`soh_${name}_failed`] = c.failed;
        }
        values.soh_failed_total = s.failed;
        values.soh_failures = failureText(s.failures) || undefined;
      }
    }

    out.push(`      <node${xmlAttrs({ id: n.id, label: n.label })}>`);
    const av = Object.entries(values)
      .map(([k, v]) => attvalue(k, nodeType_[k], v))
      .filter(Boolean);
    if (av.length) {
      out.push('        <attvalues>');
      for (const a of av) out.push(`          ${a}`);
      out.push('        </attvalues>');
    }
    out.push(`        ${vizColor(displayColor(n, running))}`);
    const p = positions.get(n.id);
    if (p && Number.isFinite(p.x) && Number.isFinite(p.y)) {
      out.push(
        `        <viz:position x="${round(p.x)}" y="${round(-p.y)}" z="0"/>`,
      );
    }
    out.push(`        <viz:size value="${NODE_SIZE[type]}"/>`);
    out.push(`        <viz:shape value="${NODE_SHAPE[type]}"/>`);
    out.push('      </node>');
  }
  for (const [ip, id] of addressNodes) {
    out.push(`      <node${xmlAttrs({ id, label: ip })}>`);
    out.push('        <attvalues>');
    out.push(`          ${attvalue('node_type', 'string', 'address')}`);
    out.push(`          ${attvalue('ip_addresses', 'liststring', [ip])}`);
    out.push('        </attvalues>');
    out.push(`        ${vizColor('#bbbbbb')}`);
    out.push(`        <viz:size value="${NODE_SIZE.address}"/>`);
    out.push(`        <viz:shape value="${NODE_SHAPE.address}"/>`);
    out.push('      </node>');
  }
  out.push('    </nodes>');

  const linkEdges = edges.filter(
    (e) => byId.has(e.source) && byId.has(e.target),
  );
  out.push(`    <edges count="${linkEdges.length + flowEdges.length}">`);
  for (const e of linkEdges) {
    const info = edgeInfo.get(e.id) ?? {};
    out.push(
      `      <edge${xmlAttrs({ id: `e${e.id}`, source: e.source, target: e.target, kind: 'interface' })}>`,
    );
    out.push('        <attvalues>');
    for (const [k, v] of [
      ['relation', 'interface'],
      ['vlan', info.vlan],
      ['interface_index', info.index],
      ['ip_address', info.ip],
    ]) {
      const a = attvalue(k, edgeType[k], v);
      if (a) out.push(`          ${a}`);
    }
    out.push('        </attvalues>');
    out.push('      </edge>');
  }
  flowEdges.forEach((f, i) => {
    out.push(
      `      <edge${xmlAttrs({ id: `f${i}`, source: f.source, target: f.target, type: 'directed', kind: 'flow', weight: f.bytes })}>`,
    );
    out.push('        <attvalues>');
    out.push(`          ${attvalue('relation', 'string', 'flow')}`);
    out.push(`          ${attvalue('bytes', 'long', f.bytes)}`);
    out.push('        </attvalues>');
    out.push('      </edge>');
  });
  out.push('    </edges>');
  out.push('  </graph>');
  out.push('</gexf>');
  return out.join('\n') + '\n';
}
