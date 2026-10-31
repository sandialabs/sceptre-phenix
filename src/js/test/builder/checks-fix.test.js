// @vitest-environment happy-dom
// Fix in the Diagram checks dialog, mounted with a real Builder store: the
// edit is one Undo step, the dialog says what changed, and focus moves on.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import BuilderChecks from '@/components/builder/BuilderChecks.vue';
import BuilderLiveRegion from '@/components/builder/BuilderLiveRegion.vue';
import { connect, findNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

// The sample diagram, with bravo renamed to alpha and both devices' eth0
// on one MAC address: a duplicate hostname and two shared MAC addresses.
// Unless connected, bravo's eth0 is on its network by its VLAN alone, which
// is a warning without a fix.
function brokenDiagram({ connected = false } = {}) {
  const sample = sampleDocument();
  const { alpha, bravo, sw } = sample;
  const doc = connected
    ? connect(sample.doc, {
        sourceNodeId: bravo.id,
        sourceHandleId: bravo.device.interfaces[0].id,
        targetNodeId: sw.id,
      }).doc
    : sample.doc;
  const mac = '02:00:00:00:00:01';

  return {
    alpha,
    bravo,
    doc: {
      ...doc,
      nodes: doc.nodes.map((node) => {
        if (node.kind !== 'device') {
          return node;
        }

        const spec = JSON.parse(JSON.stringify(node.device.spec));
        const hostname = 'alpha';

        spec.general.hostname = hostname;
        spec.network.interfaces = [
          {
            name: 'eth0',
            type: 'ethernet',
            proto: 'static',
            address: node.id === alpha.id ? '10.0.0.2' : '10.0.0.3',
            mask: 24,
            vlan: 'EXP',
            mac,
          },
        ];

        return {
          ...node,
          label: hostname,
          device: { ...node.device, hostname, spec },
        };
      }),
    },
  };
}

async function openChecks({
  readOnly = false,
  connected = false,
  pinia = createPinia(),
} = {}) {
  const store = useBuilderStore(pinia);
  const broken = brokenDiagram({ connected });

  store.doc = broken.doc;
  store.history.reset(broken.doc, 'Draft loaded');
  store.readOnly = readOnly;
  store.fetchDisks = () => {};

  const wrapper = mount(BuilderChecks, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  await wrapper.get('[data-testid="builder-checks"]').trigger('click');

  return { wrapper, store, ...broken };
}

describe('Fix in the Diagram checks', () => {
  test('is offered for each issue one edit fixes, and says what it does', async () => {
    const { wrapper } = await openChecks();
    const fixes = wrapper.findAll('[data-testid="issue-fix"]');

    expect(fixes.map((button) => button.attributes('aria-label'))).toEqual([
      'Fix: Rename alpha to alpha-2',
      'Fix: Clear the MAC address of interface eth0 of alpha',
      'Fix: Clear the MAC address of interface eth0 of alpha',
    ]);
    expect(fixes.map((button) => button.text())).toEqual(['Fix', 'Fix', 'Fix']);
    expect(wrapper.findAll('[data-testid="issue-fix-hint"]')[0].text()).toBe(
      'Fix: Rename alpha to alpha-2',
    );
  });

  test('makes the edit as one named Undo step, says so, and moves focus to the next issue', async () => {
    const { wrapper, store, bravo } = await openChecks();
    const steps = store.history.entries.length;
    const [first] = wrapper.findAll('[data-testid="issue-fix"]');

    first.element.focus();
    await first.trigger('click');

    expect(findNode(store.doc, bravo.id).device.hostname).toBe('alpha-2');
    expect(store.history.entries).toHaveLength(steps + 1);
    expect(store.history.undoLabel()).toBe('Renamed alpha to alpha-2');
    expect(wrapper.get('[data-testid="checks-fixed"]').text()).toBe(
      'Renamed alpha to alpha-2.',
    );
    expect(store.announcement).toBe('Renamed alpha to alpha-2');

    // The issue is gone, and focus is on the Fix of the one in its place.
    const fixes = wrapper.findAll('[data-testid="issue-fix"]');

    expect(fixes).toHaveLength(2);
    expect(document.activeElement).toBe(fixes[0].element);
  });

  // Clearing one MAC address leaves the other unshared, so two fixes fix
  // all three issues.
  async function fixAll(wrapper) {
    for (const button of [1, 2].map(() => '[data-testid="issue-fix"]')) {
      await wrapper.get(button).trigger('click');
    }

    expect(wrapper.find('[data-testid="issue-fix"]').exists()).toBe(false);
  }

  test('moves focus to the Go to of an issue left without a fix', async () => {
    const { wrapper } = await openChecks();

    await fixAll(wrapper);

    const items = wrapper.findAll('[data-testid="issue"]');

    expect(items.map((item) => item.get('code').text())).toEqual([
      'interface.vlan.implicit',
    ]);
    expect(document.activeElement).toBe(
      items[0].get('[data-testid="issue-go-to"]').element,
    );
  });

  test('moves focus to the summary when no issue is left', async () => {
    const { wrapper } = await openChecks({ connected: true });

    await fixAll(wrapper);

    expect(wrapper.findAll('[data-testid="issue"]')).toHaveLength(0);
    expect(wrapper.get('[data-testid="checks-summary"]').text()).toBe(
      'This diagram has no errors or warnings.',
    );
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="checks-summary"]').element,
    );
  });

  test('clears what it said when the dialog closes', async () => {
    const { wrapper } = await openChecks();

    await wrapper.get('[data-testid="issue-fix"]').trigger('click');
    expect(wrapper.get('[data-testid="checks-fixed"]').text()).not.toBe('');

    await wrapper.get('button[aria-label="Close dialog"]').trigger('click');
    await wrapper.get('[data-testid="builder-checks"]').trigger('click');

    expect(wrapper.get('[data-testid="checks-fixed"]').text()).toBe('');
  });

  test('is not said again by the page after the dialog closes', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    // happy-dom parses :modal but matches no element. The open dialog is
    // modal.
    const query = document.querySelector.bind(document);

    vi.spyOn(document, 'querySelector').mockImplementation((selector) =>
      query(selector === 'dialog:modal' ? 'dialog[open]' : selector),
    );

    try {
      const pinia = createPinia();
      const page = mount(BuilderLiveRegion, {
        attachTo: document.body,
        global: { plugins: [pinia] },
      });
      const { wrapper, store } = await openChecks({ pinia });
      const region = () =>
        page.get('[data-testid="builder-live-region"]').text();
      const spoken = [];
      const listen = async () => {
        for (let step = 0; step < 4; step += 1) {
          vi.advanceTimersByTime(750);
          await page.vm.$nextTick();

          if (region() && region() !== spoken.at(-1)) {
            spoken.push(region());
          }
        }
      };

      await wrapper.get('[data-testid="issue-fix"]').trigger('click');
      expect(wrapper.get('[data-testid="checks-fixed"]').text()).toBe(
        'Renamed alpha to alpha-2.',
      );
      // Said by the store while the dialog is open, but not by the dialog.
      store.announce('Copied 1 node');

      await wrapper.get('button[aria-label="Close dialog"]').trigger('click');
      await listen();

      expect(spoken).toEqual(['Copied 1 node']);
    } finally {
      vi.useRealTimers();
      vi.restoreAllMocks();
    }
  });

  test('is not offered on a read-only draft', async () => {
    const { wrapper } = await openChecks({ readOnly: true });

    expect(wrapper.findAll('[data-testid="issue"]').length).toBeGreaterThan(0);
    expect(wrapper.find('[data-testid="issue-fix"]').exists()).toBe(false);
  });
});
