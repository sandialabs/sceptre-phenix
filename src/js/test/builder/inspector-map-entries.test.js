// @vitest-environment happy-dom
// A label's or an annotation's row in the Inspector: Go to a check about it
// focuses its value, and the values of ntp-server and vrouter/enable-ssh
// suggest the names of the device's interfaces.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises } from '@vue/test-utils';

import { closeSections } from '@/components/builder/inspector/InspectorSectionRenderer.vue';

import { updateNode } from '@/builder/model.js';

import { sampleDocument } from './fixtures.js';
import { mountInspector } from './inspector-mount.js';

enableAutoUnmount(afterEach);
afterEach(closeSections);

// The Inspector on the sample's alpha device, with labels and annotations.
async function openAlpha() {
  const sample = sampleDocument();
  const { spec } = sample.alpha.device;
  const doc = updateNode(sample.doc, sample.alpha.id, {
    device: {
      spec: {
        ...spec,
        network: {
          interfaces: [
            ...spec.network.interfaces,
            { name: 'mgmt0', type: 'ethernet', proto: 'dhcp', vlan: 'EXP' },
          ],
        },
        labels: { 'disable-injects': 'true', 'ntp-server': 'eth9' },
        annotations: { 'vrouter/enable-ssh': 'eth0', note: 'x' },
      },
    },
  });
  const opened = await mountInspector({ doc, element: sample.alpha });

  return { ...opened, ...sample };
}

// The value field of the row of `key` in the map at `path`.
function valueOf(wrapper, path, key) {
  return wrapper.get(`[data-entry-path="${path}.${key}"] input`);
}

describe('label and annotation rows', () => {
  test('Go to a check about a label focuses its value', async () => {
    const { wrapper, store, alpha } = await openAlpha();
    const announce = vi.spyOn(store, 'announce');

    expect(
      store.goToIssue({
        code: 'device.ntp-server.invalid',
        path: 'nodes[1].device.spec.labels.ntp-server',
        field: 'spec.labels.ntp-server',
        message: 'the ntp-server label names no interface',
        nodeId: alpha.id,
      }),
    ).toBe(true);
    await flushPromises();

    expect(document.activeElement).toBe(
      valueOf(wrapper, 'spec.labels', 'ntp-server').element,
    );
    expect(announce).toHaveBeenCalledWith(
      'ntp-server (Labels) in device alpha',
    );
  });

  test('ntp-server and vrouter/enable-ssh suggest the interface names', async () => {
    const { wrapper } = await openAlpha();
    const suggested = (input) => {
      const id = input.attributes('list');

      return id
        ? [...document.getElementById(id).querySelectorAll('option')].map(
            (option) => option.value,
          )
        : null;
    };

    expect(suggested(valueOf(wrapper, 'spec.labels', 'ntp-server'))).toEqual([
      'eth0',
      'mgmt0',
    ]);
    expect(
      suggested(valueOf(wrapper, 'spec.annotations', 'vrouter/enable-ssh')),
    ).toEqual(['eth0', 'mgmt0']);
    expect(
      suggested(valueOf(wrapper, 'spec.labels', 'disable-injects')),
    ).toBeNull();
    expect(suggested(valueOf(wrapper, 'spec.annotations', 'note'))).toBeNull();
  });
});
