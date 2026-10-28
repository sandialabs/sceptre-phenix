// @vitest-environment happy-dom
// Edit on a topology made in the Builder opens the Builder for a role that
// has it, and shows an alert for a role that does not.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import Buefy from 'buefy';
import ConfigsEditor from '@/components/configs/ConfigsEditor.vue';

const site = {
  apiVersion: 'phenix.sandia.gov/v1',
  kind: 'Topology',
  metadata: { name: 'site', annotations: { 'builder-doc': '{}' } },
  spec: { nodes: [] },
};

vi.mock('@/utils/axios.js', () => ({
  default: {
    get: vi.fn(() => Promise.resolve({ data: structuredClone(site) })),
  },
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));
const phenix = vi.hoisted(() => ({ role: null }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix }));

// roleAllowed caches its answers by role name, so every role gets its own.
let roles = 0;
function setRole(resources) {
  roles += 1;
  phenix.role = {
    name: `editor-role-${roles}`,
    policies: [{ resources, verbs: ['*'], resourceNames: ['*/*'] }],
  };
}

enableAutoUnmount(afterEach);

function mountEditor(router) {
  return mount(ConfigsEditor, {
    attachTo: document.body,
    props: { mode: 'edit', editorConfig: structuredClone(site) },
    global: {
      plugins: [Buefy],
      stubs: { AceEditor: true },
      mocks: { $router: router },
    },
  });
}

const alertText = 'Only Builder can edit this configuration';

describe('ConfigsEditor Edit on a Builder topology', () => {
  let router;

  beforeEach(() => {
    router = { push: vi.fn(() => Promise.resolve()) };
    document.body.innerHTML = '';
  });

  test('a role with the Builder goes to the Builder, with no alert', async () => {
    setRole(['*']);
    const wrapper = mountEditor(router);
    await flushPromises();

    expect(router.push).toHaveBeenCalledTimes(1);
    expect(wrapper.emitted('is-done')).toEqual([['']]);
    expect(document.body.textContent).not.toContain(alertText);
  });

  test('a role without the Builder gets the alert and stays on Configs', async () => {
    setRole(['configs']);
    const wrapper = mountEditor(router);
    await flushPromises();

    expect(router.push).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain(alertText);
    expect(document.body.textContent).toContain('Built by Builder');
    // The editor stays open behind the alert until the user closes it.
    expect(wrapper.emitted('is-done')).toBeUndefined();

    const ok = [...document.querySelectorAll('.modal-card button')].find(
      (button) => button.textContent.trim() === 'OK',
    );
    ok.click();
    await flushPromises();

    expect(wrapper.emitted('is-done')).toEqual([['']]);
    expect(router.push).not.toHaveBeenCalled();
  });
});
