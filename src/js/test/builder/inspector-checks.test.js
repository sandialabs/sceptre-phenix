// The Inspector's own checks: the section at its top, and the warnings it
// provides to the fields of the working copy (INSPECTOR_FIELD_WARNINGS).

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';

import { useBuilderStore } from '@/builder/store.js';
import { deviceFieldWarnings } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

// A probe in place of the fields that get warnings (VLAN, drive image, IP
// and MAC address): it renders the warnings the Inspector provides for the
// field's JSON Forms path.
vi.mock('@/builder/adapters/forms.js', async (importOriginal) => {
  const actual = await importOriginal();
  const { defineComponent, h: render } = await import('vue');
  const { rendererProps, useJsonFormsControl } = await import('@jsonforms/vue');
  const { or, rankWith, scopeEndsWith } = await import('@jsonforms/core');
  const { useInspectorFieldWarnings } = await import(
    '@/components/builder/inspector/control.js'
  );
  const Probe = defineComponent({
    props: rendererProps(),
    setup(props) {
      const { control } = useJsonFormsControl(props);
      const warnings = useInspectorFieldWarnings();

      return () =>
        render(
          'output',
          { 'data-probe': control.value.path },
          (warnings.value[control.value.path] || []).join(' | '),
        );
    },
  });

  return {
    ...actual,
    inspectorRenderers: [
      ...actual.inspectorRenderers,
      {
        tester: rankWith(
          100,
          or(
            scopeEndsWith('vlan'),
            scopeEndsWith('image'),
            scopeEndsWith('address'),
            scopeEndsWith('mac'),
          ),
        ),
        renderer: Probe,
      },
    ],
  };
});

// The sample, with an interface on no network of the diagram and a drive
// the server has no image for added to alpha.
function sample() {
  const result = sampleDocument();
  const doc = {
    ...result.doc,
    nodes: result.doc.nodes.map((node) => {
      if (node.id !== result.alpha.id) {
        return node;
      }

      const copy = JSON.parse(JSON.stringify(node));

      copy.device.spec.network.interfaces.push({
        name: 'eth1',
        type: 'ethernet',
        proto: 'dhcp',
        vlan: 'GHOST',
      });
      copy.device.spec.hardware.drives.push({ image: 'gone.qc2' });

      return copy;
    }),
  };

  return { ...result, doc };
}

async function renderInspector(doc, selection, { disks = null } = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(BuilderInspector) });
  app.use(pinia);

  const store = useBuilderStore(pinia);
  store.doc = doc;
  store.disks = disks;
  store.select(selection);

  return renderToString(app);
}

function probe(html, path) {
  const match = new RegExp(
    `<output data-probe="${path.replace(/\./g, '\\.')}">([^<]*)</output>`,
  ).exec(html);

  return match ? match[1].replace(/&quot;/g, '"') : null;
}

describe('Inspector checks', () => {
  test('the section at the top lists the selection’s own issues', async () => {
    const { doc, alpha } = sample();
    const html = await renderInspector(
      doc,
      { nodes: [alpha.id] },
      { disks: ['ubuntu.qc2'] },
    );
    const section = /data-testid="inspector-checks"[\s\S]*?<\/ul>/.exec(
      html,
    )?.[0];

    expect(section).toContain('Checks: 3 warnings');
    expect(section).toContain('uses VLAN &quot;GHOST&quot;');
    expect(section).toContain(
      'interface &quot;eth1&quot; of &quot;alpha&quot; is not connected to a network',
    );
    expect(section).toContain('drive image &quot;gone.qc2&quot;');
    // Not bravo's unconnected interface, and above the form.
    expect(section).not.toContain('bravo');
    expect(html.indexOf('inspector-checks')).toBeLessThan(
      html.indexOf('Hostname'),
    );
  });

  test('the section is left out when the selection has no issues', async () => {
    const { doc, alpha } = sampleDocument();
    const html = await renderInspector(doc, { nodes: [alpha.id] });

    expect(html).not.toContain('inspector-checks');
  });

  test('the fields get the warnings of the working copy by form path', async () => {
    const { doc, alpha, bravo } = sample();
    const html = await renderInspector(
      doc,
      { nodes: [alpha.id] },
      { disks: ['ubuntu.qc2'] },
    );

    expect(probe(html, 'spec.network.interfaces.0.vlan')).toBe('');
    expect(probe(html, 'spec.network.interfaces.1.vlan')).toBe(
      'No network in this diagram is named "GHOST".',
    );
    expect(probe(html, 'spec.hardware.drives.0.image')).toBe('');
    expect(probe(html, 'spec.hardware.drives.1.image')).toBe(
      'The server has no disk image named "gone.qc2".',
    );

    // Drive images go unchecked while the server's are unknown.
    const unknown = await renderInspector(doc, { nodes: [alpha.id] });

    expect(probe(unknown, 'spec.hardware.drives.1.image')).toBe('');
    expect(
      probe(
        await renderInspector(doc, { nodes: [bravo.id] }, { disks: [] }),
        'spec.hardware.drives.0.image',
      ),
    ).toBe('The server has no disk image named "ubuntu.qc2".');
  });
});

// A warning at a field the Inspector does not show could be neither seen
// nor fixed there: each field that gets one is rendered, whatever kind of
// interface it is on.
describe('address warnings', () => {
  test('every warned field is one the Inspector shows', async () => {
    const result = sampleDocument();
    const mac = 'aa:bb:cc:dd:ee:ff';
    const kinds = {
      static: { proto: 'static', address: '10.0.0.5', mask: 24, mac },
      dhcp: { proto: 'dhcp', address: '10.0.0.5', mac },
      manual: { proto: 'manual', address: '10.0.0.5', mask: 24, mac },
      qinq: { proto: 'static', address: '10.0.0.5', mask: 24, qinq: true },
    };
    const doc = {
      ...result.doc,
      nodes: result.doc.nodes.map((node) => {
        if (node.id === result.alpha.id) {
          const copy = JSON.parse(JSON.stringify(node));

          copy.device.spec.network.interfaces = Object.values(kinds).map(
            (fields, index) => ({
              name: `eth${index}`,
              type: 'ethernet',
              vlan: 'EXP',
              ...fields,
            }),
          );

          return copy;
        }

        if (node.id === result.bravo.id) {
          const copy = JSON.parse(JSON.stringify(node));

          copy.device.spec = {
            ...copy.device.spec,
            external: true,
            type: 'HIL',
          };
          copy.device.spec.network.interfaces = [
            { name: 'eth0', vlan: 'EXP', ...kinds.static },
          ];

          return copy;
        }

        return node;
      }),
    };
    const warned = {};

    for (const node of [result.alpha, result.bravo]) {
      const { spec } = doc.nodes.find((item) => item.id === node.id).device;
      const warnings = deviceFieldWarnings(doc, spec, { nodeId: node.id });
      const html = await renderInspector(doc, { nodes: [node.id] });

      warned[node.device.hostname] = Object.keys(warnings).sort();

      for (const [path, messages] of Object.entries(warnings)) {
        expect(probe(html, path), path).toBe(messages.join(' | '));
      }
    }

    // A manual interface's address, and an external device's MAC, are not
    // compared; the static and QinQ interfaces' addresses and the static,
    // DHCP and manual interfaces' MACs are.
    expect(warned).toEqual({
      alpha: [
        'spec.network.interfaces.0.address',
        'spec.network.interfaces.0.mac',
        'spec.network.interfaces.1.mac',
        'spec.network.interfaces.2.mac',
        'spec.network.interfaces.3.address',
      ],
      bravo: ['spec.network.interfaces.0.address'],
    });
  });
});
