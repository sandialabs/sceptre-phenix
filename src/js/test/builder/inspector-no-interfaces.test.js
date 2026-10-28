// @vitest-environment happy-dom
// A device with no interfaces: its warning names the Interfaces list, so Go
// to, from the checks or from the Inspector, focuses Add interface.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import { closeSections } from '@/components/builder/inspector/InspectorSectionRenderer.vue';

import { addNode } from '@/builder/model.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';
import { mountInspector } from './inspector-mount.js';

enableAutoUnmount(afterEach);
afterEach(closeSections);

const CODE = 'device.interfaces.none';

// The Inspector on a new device, lonely, which has no interfaces.
async function openLonely() {
  const added = addNode(sampleDocument().doc, {
    kind: 'device',
    hostname: 'lonely',
    position: { x: 400, y: 400 },
  });
  const opened = await mountInspector({ doc: added.doc, element: added.node });
  const warning = validateDocument(added.doc).find(
    (issue) => issue.code === CODE && issue.message.includes('"lonely"'),
  );

  return { ...opened, node: added.node, warning };
}

describe('a device with no interfaces', () => {
  test('its warning names the Interfaces list', async () => {
    const { warning } = await openLonely();

    expect(warning).toMatchObject({
      code: CODE,
      level: 'warning',
      field: 'spec.network.interfaces',
    });
  });

  test('Go to from the checks focuses Add interface', async () => {
    const { wrapper, store, node, warning } = await openLonely();
    const announce = vi.spyOn(store, 'announce');

    expect(store.goToIssue({ ...warning, nodeId: node.id })).toBe(true);
    await flushPromises();

    const add = wrapper.get(
      '[data-path="spec.network.interfaces"] .array-list-add',
    );

    expect(document.activeElement).toBe(add.element);
    expect(announce).toHaveBeenCalledWith('Interfaces in device lonely');
  });

  test('the Inspector’s own checks have Go to for it', async () => {
    const { wrapper } = await openLonely();
    const goTo = wrapper
      .findAll('[data-testid="inspector-check-go-to"]')
      .find((button) =>
        button.attributes('aria-label').includes('has no interfaces'),
      );

    expect(goTo).toBeDefined();
    await goTo.trigger('click');
    await flushPromises();

    expect(document.activeElement).toBe(
      wrapper.get('[data-path="spec.network.interfaces"] .array-list-add')
        .element,
    );
  });
});
