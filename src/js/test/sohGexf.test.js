// GEXF 1.3 export of the State of Health graph.
import { execFileSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';

import { buildGexf } from '@/utils/soh/gexf.js';
import { sohFixture } from './sohFixture.js';

// Checks tags nest and close, attributes are quoted and no raw < or & is
// left in text: enough to catch a broken writer without an XML library.
function checkWellFormed(xml) {
  const stack = [];
  const body = xml.replace(/^<\?xml[^?]*\?>\s*/, '');
  const re =
    /<(\/?)([A-Za-z_][\w:.-]*)((?:\s+[\w:.-]+="[^"<]*")*)\s*(\/?)>|([^<]+)/gy;
  let m;
  let pos = 0;
  while ((m = re.exec(body))) {
    pos = re.lastIndex;
    if (m[5] !== undefined) {
      expect(m[5]).not.toMatch(/&(?!(amp|lt|gt|quot|apos|#\d+);)/);
      continue;
    }
    for (const [, v] of m[3].matchAll(/="([^"]*)"/g)) {
      expect(v).not.toMatch(/&(?!(amp|lt|gt|quot|apos|#\d+);)/);
    }
    if (m[1]) expect(stack.pop()).toBe(m[2]);
    else if (!m[4]) stack.push(m[2]);
  }
  expect(pos).toBe(body.length);
  expect(stack).toEqual([]);
}

const hasXmllint = (() => {
  try {
    execFileSync('xmllint', ['--version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
})();

function fixture() {
  const soh = sohFixture();
  const dmz0 = soh.nodes.find((n) => n.label === 'dmz-0');
  dmz0.label = `dmz-0 <a&b "q" 'x'>`;
  dmz0.status = 'running';
  dmz0.tags = {
    role: 'web, public',
    team: 'blue',
    __notes_1: 'line one\nline two',
    __sohStyle: 'fill: #ff0000; ',
  };
  dmz0.soh = {
    hostname: dmz0.label,
    cpuLoad: '0.42 0.30 0.10',
    errors: true,
    reachability: [
      {
        metadata: { host: 'dmz-0', target: '10.0.0.9' },
        timestamp: '2026-09-28 10:00:00',
        success: '',
        error: 'timeout <script>alert(1)</script>\u0001',
      },
      {
        metadata: { host: 'dmz-0', target: '10.0.0.1' },
        timestamp: '2026-09-28 10:00:01',
        success: 'ok',
        error: '',
      },
    ],
    processes: [
      {
        metadata: { proc: 'nginx' },
        timestamp: '2026-09-28 10:00:02',
        success: 'running',
        error: '',
      },
    ],
    listeners: [
      {
        metadata: { port: 443 },
        timestamp: '2026-09-28 10:00:03',
        success: '',
        error: 'not listening',
      },
    ],
    customTests: [
      {
        metadata: { test: 'db "ping"' },
        timestamp: '2026-09-28 10:00:04',
        success: '',
        error: 'exit 1',
      },
    ],
  };
  const historian = soh.nodes.find((n) => n.label === 'historian');
  const vms = [
    {
      name: dmz0.label,
      host: 'compute1',
      ipv4: ['10.1.0.10'],
      cpus: 2,
      ram: 4096,
      disk: '/phenix/images/ubuntu.qc2',
      networks: ['MGMT (10)', 'dmz (101)'],
      state: 'RUNNING',
      running: true,
      uptime: 3600.5,
      dnb: false,
      external: false,
      description: 'web server',
    },
    {
      name: 'historian',
      ipv4: ['10.2.0.5', '10.3.0.5'],
      networks: ['corp', 'ot'],
      state: 'RUNNING',
      running: true,
    },
  ];
  const positions = new Map(
    soh.nodes.map((n, i) => [n.id, { x: i * 10, y: i * 5 }]),
  );
  return { soh, dmz0, historian, vms, positions };
}

describe('GEXF export', () => {
  const { soh, dmz0, historian, vms, positions } = fixture();
  const now = new Date('2026-09-28T12:34:56Z');
  const xml = buildGexf({
    experiment: 'exp <1> & co',
    nodes: soh.nodes,
    edges: soh.edges,
    positions,
    running: true,
    vms,
    hosts: ['10.1.0.10', '10.3.0.5', '8.8.8.8'],
    flows: [
      [0, 1500, 20],
      [700, 0, 0],
      [0, 0, 0],
    ],
    layout: 'Layered',
    filter: 'all',
    now,
  });

  it('is a well-formed GEXF 1.3 document', () => {
    expect(
      xml.startsWith(
        '<?xml version="1.0" encoding="UTF-8"?>\n<gexf xmlns="http://gexf.net/1.3"',
      ),
    ).toBe(true);
    expect(xml).toContain('xmlns:viz="http://gexf.net/1.3/viz"');
    expect(xml).toMatch(/<gexf [^>]*version="1\.3">/);
    checkWellFormed(xml);
  });

  it.skipIf(!hasXmllint)('parses with xmllint', () => {
    expect(() =>
      execFileSync('xmllint', ['--noout', '-'], { input: xml, stdio: 'pipe' }),
    ).not.toThrow();
  });

  it('describes the export in <meta>', () => {
    expect(xml).toContain('<meta lastmodifieddate="2026-09-28">');
    expect(xml).toContain('<creator>phenix</creator>');
    expect(xml).toContain(
      'experiment exp &lt;1&gt; &amp; co: 19 VMs and 4 VLANs',
    );
    expect(xml).toContain('layout: Layered');
  });

  it('declares typed node and edge attributes', () => {
    expect(xml).toContain('<attributes class="node">');
    expect(xml).toContain('<attributes class="edge">');
    for (const [id, type] of [
      ['status', 'string'],
      ['ip_addresses', 'liststring'],
      ['vlans', 'liststring'],
      ['cpus', 'integer'],
      ['external', 'boolean'],
      ['do_not_boot', 'boolean'],
      ['cpu_load_1m', 'double'],
      ['soh_reachability_ok', 'integer'],
      ['soh_reachability_failed', 'integer'],
      ['soh_custom_tests_failed', 'integer'],
      ['soh_failures', 'string'],
      ['interface_index', 'integer'],
      ['bytes', 'long'],
    ]) {
      expect(xml).toContain(
        `<attribute id="${id}" title="${id}" type="${type}"/>`,
      );
    }
    expect(xml).toContain(`<nodes count="${soh.nodes.length + 1}">`);
    expect(xml).toContain(`<edges count="${soh.edges.length + 3}">`);
  });

  const nodeXml = (id) => {
    const m = new RegExp(`<node id="${id}"[\\s\\S]*?</node>`).exec(xml);
    return m?.[0] ?? '';
  };

  it('escapes labels, tags and SOH text', () => {
    const n = nodeXml(dmz0.id);
    expect(n).toContain(
      'label="dmz-0 &lt;a&amp;b &quot;q&quot; &apos;x&apos;&gt;"',
    );
    expect(n).not.toContain('<script>');
    expect(n).not.toContain('\u0001');
    expect(n).toContain('value="[&quot;role=web, public&quot;, team=blue]"');
    expect(n).toContain(
      '<attvalue for="notes" value="line one&#10;line two"/>',
    );
  });

  it('carries VM details and SOH results', () => {
    const n = nodeXml(dmz0.id);
    expect(n).toContain('<attvalue for="node_type" value="vm"/>');
    expect(n).toContain('<attvalue for="os_type" value="linux"/>');
    expect(n).toContain('<attvalue for="status" value="running"/>');
    expect(n).toContain('<attvalue for="host" value="compute1"/>');
    expect(n).toContain('<attvalue for="ip_addresses" value="[10.1.0.10]"/>');
    expect(n).toContain('<attvalue for="vlans" value="[MGMT, dmz]"/>');
    expect(n).toContain('<attvalue for="memory_mb" value="4096"/>');
    expect(n).toContain('<attvalue for="cpu_load_1m" value="0.42"/>');
    expect(n).toContain('<attvalue for="soh_reachability_ok" value="1"/>');
    expect(n).toContain('<attvalue for="soh_reachability_failed" value="1"/>');
    expect(n).toContain('<attvalue for="soh_processes_ok" value="1"/>');
    expect(n).toContain('<attvalue for="soh_failed_total" value="3"/>');
    expect(n).toMatch(
      /for="soh_failures" value="reachability 10\.0\.0\.9: timeout &lt;script&gt;.*; listeners 443: not listening; custom tests db &quot;ping&quot;: exit 1"/,
    );
    expect(n).toContain(
      '<attvalue for="soh_last_check" value="2026-09-28 10:00:04"/>',
    );
  });

  it('draws nodes as the graph does: color, position, size and shape', () => {
    // the custom fill wins over the status color
    expect(nodeXml(dmz0.id)).toContain(
      '<viz:color r="255" g="0" b="0" hex="#ff0000"/>',
    );
    const rtr = nodeXml(1);
    expect(rtr).toContain('<viz:color r="79" g="143" b="0" hex="#4f8f00"/>');
    expect(rtr).toContain('<viz:shape value="diamond"/>');
    const i = soh.nodes.findIndex((n) => n.id === 1);
    expect(rtr).toContain(`<viz:position x="${i * 10}" y="${-i * 5}" z="0"/>`);
    const vlan = nodeXml(101);
    expect(vlan).toContain('<viz:shape value="square"/>');
    expect(vlan).toContain('hex="#9aa7b5"');
    expect(vlan).toContain('<attvalue for="node_type" value="vlan"/>');
  });

  it('labels interface edges with their VLAN, index and address', () => {
    const e = soh.edges.find((x) => x.source === dmz0.id);
    const m = new RegExp(`<edge id="e${e.id}"[\\s\\S]*?</edge>`).exec(xml)[0];
    expect(m).toContain('kind="interface"');
    expect(m).toContain('<attvalue for="vlan" value="dmz"/>');
    // MGMT is interface 0, so dmz is interface 1 and has no address listed
    expect(m).toContain('<attvalue for="interface_index" value="1"/>');
    const hOt = soh.edges.find(
      (x) => x.source === historian.id && x.target === 103,
    );
    const h = new RegExp(`<edge id="e${hOt.id}"[\\s\\S]*?</edge>`).exec(xml)[0];
    expect(h).toContain('<attvalue for="ip_address" value="10.3.0.5"/>');
  });

  it('adds traffic flows as weighted directed edges', () => {
    expect(xml).toContain(
      `<edge id="f0" source="${dmz0.id}" target="${historian.id}" type="directed" kind="flow" weight="1500">`,
    );
    expect(xml).toContain(
      `source="${dmz0.id}" target="ip:8.8.8.8" type="directed" kind="flow" weight="20"`,
    );
    expect(xml).toContain(
      `source="${historian.id}" target="${dmz0.id}" type="directed" kind="flow" weight="700"`,
    );
    expect(xml).toContain('<node id="ip:8.8.8.8" label="8.8.8.8">');
  });

  it('exports without the VM list when it cannot be read', () => {
    const out = buildGexf({
      experiment: 'x',
      nodes: soh.nodes,
      edges: soh.edges,
    });
    checkWellFormed(out);
    expect(out).toContain(
      'VM details (host, addresses, CPUs, memory, disk) not available',
    );
    expect(out).not.toContain('viz:position');
    // interfaces fall back to the order of the VM's edges
    expect(out).toMatch(/<attvalue for="vlans" value="\[corp, ot\]"\/>/);
  });
});

describe('GEXF values', () => {
  // the <node> of one VM with these tags and SOH results
  const exported = (tags, soh = null) => {
    const node = { id: 1, label: 'web', image: 'linux', status: 'running' };
    const xml = buildGexf({
      experiment: 'x',
      nodes: [{ ...node, tags, soh }],
      edges: [],
    });
    return /<node id="1"[\s\S]*?<\/node>/.exec(xml)[0];
  };

  it('writes 1.3 lists, quoting items that need it', () => {
    expect(exported({ a: 'b c', q: 'x,y', r: '[z]', s: 'q"' })).toContain(
      '<attvalue for="tags" value="[a=b c, &quot;q=x,y&quot;, &quot;r=[z]&quot;, &quot;s=q\\&quot;&quot;]"/>',
    );
    // an empty list is left out
    expect(exported({})).not.toContain('for="tags"');
  });

  it('counts passed and failed checks per category', () => {
    const n = exported(
      {},
      {
        networking: [{ error: '' }, { error: 'down', metadata: {} }],
        filesAbsent: [{ error: 'present', metadata: { path: '/tmp/x' } }],
      },
    );
    for (const [id, value] of [
      ['soh_networking_ok', 1],
      ['soh_networking_failed', 1],
      ['soh_files_absent_ok', 0],
      ['soh_files_absent_failed', 1],
      ['soh_reachability_ok', 0],
      ['soh_reachability_failed', 0],
      ['soh_failed_total', 2],
      ['soh_errors', true],
      ['soh_failures', 'networking: down; files absent /tmp/x: present'],
    ]) {
      expect(n).toContain(`<attvalue for="${id}" value="${value}"/>`);
    }
  });
});
