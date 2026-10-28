// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import {
  RouterLinkStub,
  enableAutoUnmount,
  flushPromises,
  mount,
} from '@vue/test-utils';
import Buefy from 'buefy';
import axiosInstance from '@/utils/axios.js';
import { useErrorNotification } from '@/utils/errorNotif';
import ConfigsList from '@/components/configs/ConfigsList.vue';

const site = {
  kind: 'Topology',
  metadata: { name: 'site', annotations: { 'builder-doc': '{}' } },
};

// The list holds one topology made in the Builder, and viewing it reads it
// again. roleAllowed reads the role from the mocked Pinia store.
vi.mock('@/utils/axios.js', () => ({
  default: {
    get: vi.fn((url) =>
      Promise.resolve({
        data: url === 'configs' ? { configs: [site] } : structuredClone(site),
      }),
    ),
    post: vi.fn(),
  },
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));
const phenix = vi.hoisted(() => ({ role: null }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix }));

const validation = [
  'nodes[0] "ADServer" (line 14): property "image" is missing (at hardware.drives[0].image)',
  '  hint: "image:" is on line 12 under nodes[0].hardware, but the schema expects it at nodes[0].hardware.drives[0].image',
].join('\n');

const closedDialog = { modal: false, title: null, msg: null };

const duplicate = {
  response: {
    status: 400,
    data: { message: 'config with same name already exists' },
  },
};

// roleAllowed caches its answers by role name, so every role gets its own.
let roles = 0;
function setRole(resources) {
  roles += 1;
  phenix.role = {
    name: `role-${roles}`,
    policies: [{ resources, verbs: ['*'], resourceNames: ['*/*'] }],
  };
}

enableAutoUnmount(afterEach);

// Mounts the list with the real Buefy. A row's Builder tag is a router-link,
// and the test has no router.
function mountList(warnings = [], props = {}) {
  return mount(ConfigsList, {
    attachTo: document.body,
    props,
    global: {
      plugins: [Buefy],
      stubs: { 'router-link': RouterLinkStub },
      config: { warnHandler: (msg) => warnings.push(msg) },
    },
  });
}

// Opens the uploader and chooses a config in its file field.
async function upload(wrapper) {
  await wrapper.get('[aria-label="Upload a new config"]').trigger('click');
  const field = wrapper.get('input[type="file"]');

  Object.defineProperty(field.element, 'files', {
    configurable: true,
    value: [new File(['kind: Topology\n'], 'topology.yml')],
  });
  await field.trigger('change');
  await flushPromises();
}

// The modal card of the validation error dialog, or undefined.
const errorCard = (wrapper) =>
  wrapper
    .findAll('.modal-card')
    .find((card) => card.find('[aria-label="Validation errors"]').exists());

describe('ConfigsList upload errors', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setRole(['*']);
  });

  test('shows no validation error dialog until an upload fails', async () => {
    const wrapper = mountList();
    await flushPromises();

    expect(errorCard(wrapper)).toBeUndefined();
    expect(wrapper.text()).not.toContain('Validation Error');
    expect(wrapper.vm.error).toEqual(closedDialog);
  });

  test('opens the dialog with the validation lines from a 400', async () => {
    axiosInstance.post.mockRejectedValueOnce({
      response: { status: 400, data: { metadata: { validation } } },
    });
    const wrapper = mountList();
    await flushPromises();

    await upload(wrapper);

    expect(axiosInstance.post).toHaveBeenCalledWith(
      'configs',
      expect.any(FormData),
    );
    expect(wrapper.vm.error).toEqual({
      modal: true,
      title: 'Validation Error',
      msg: validation,
    });
    expect(useErrorNotification).not.toHaveBeenCalled();
  });

  test.each([
    ['a 400 without validation metadata', duplicate],
    ['a network error', new Error('Network Error')],
  ])('sends %s to the error notification', async (_, err) => {
    axiosInstance.post.mockRejectedValueOnce(err);
    const wrapper = mountList();
    await flushPromises();

    await upload(wrapper);

    expect(useErrorNotification).toHaveBeenCalledWith(err);
    expect(errorCard(wrapper)).toBeUndefined();
    expect(wrapper.vm.error).toEqual(closedDialog);
  });

  test('Exit closes the dialog and clears the error', async () => {
    axiosInstance.post.mockRejectedValueOnce({
      response: { status: 400, data: { metadata: { validation } } },
    });
    const wrapper = mountList();
    await flushPromises();
    await upload(wrapper);

    await errorCard(wrapper).get('footer button').trigger('click');

    // Buefy hides the modal at once and removes it after its transition.
    const card = errorCard(wrapper);
    expect(card === undefined || !card.isVisible()).toBe(true);
    expect(wrapper.vm.error).toEqual(closedDialog);
  });

  test('renders the dialog with the lines only while it is open', async () => {
    axiosInstance.post.mockRejectedValueOnce({
      response: { status: 400, data: { metadata: { validation } } },
    });
    const warnings = [];
    const wrapper = mountList(warnings);
    await flushPromises();

    // Mounting only fetches the config list.
    expect(axiosInstance.get.mock.calls).toEqual([['configs']]);
    expect(axiosInstance.post).not.toHaveBeenCalled();
    expect(wrapper.find('textarea').exists()).toBe(false);

    await upload(wrapper);
    const card = errorCard(wrapper);
    const textarea = card.get('textarea');

    // Buefy is real and router-link has a stub, so Vue warned about
    // nothing, not even "Failed to resolve component".
    expect(warnings).toEqual([]);
    expect(card.isVisible()).toBe(true);
    expect(card.get('.modal-card-title').text()).toBe('Validation Error');
    expect(card.get('footer button').text()).toBe('Exit');
    expect(textarea.attributes()).toHaveProperty('readonly');
    expect(textarea.attributes('style')).toContain('font-family: monospace');
    expect(textarea.element.value).toBe(validation);
  });
});

describe('ConfigsList viewer', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  const nameButton = (wrapper) =>
    wrapper.get('[data-config-view="Topology/site"]');

  // Views the topology from its name, and resolves to the viewer's text.
  async function view(wrapper) {
    await nameButton(wrapper).trigger('click');
    await flushPromises();
    return wrapper.get('[aria-label="Config, as YAML"]');
  }

  test('the name is a button that opens a dialog named for the config', async () => {
    setRole(['*']);
    const wrapper = mountList();
    await flushPromises();

    const name = nameButton(wrapper);
    expect(name.element.tagName).toBe('BUTTON');
    expect(name.attributes('aria-label')).toBe('View Topology site');

    // The server's copy of the topology has a legacy Builder diagram.
    axiosInstance.get.mockResolvedValueOnce({
      data: {
        kind: 'Topology',
        metadata: {
          name: 'site',
          annotations: { 'builder-xml': '<mxGraphModel/>' },
        },
      },
    });
    const textarea = await view(wrapper);
    const dialog = wrapper.get('[role="dialog"]');
    expect(dialog.attributes('aria-label')).toBe('Topology/site');
    expect(dialog.attributes('aria-modal')).toBe('true');
    expect(textarea.isVisible()).toBe(true);
    // The legacy diagram is left out of the text, and the viewer opens.
    expect(textarea.element.value).toContain('builder-xml: <SNIPPED>');
    expect(textarea.element.value).not.toContain('mxGraphModel');
    expect(useErrorNotification).not.toHaveBeenCalled();
  });

  test('Exit returns focus to the name the viewer was opened from', async () => {
    setRole(['*']);
    const wrapper = mountList();
    await flushPromises();
    await view(wrapper);

    const exit = wrapper
      .findAll('footer button')
      .find((button) => button.text() === 'Exit');
    await exit.trigger('click');
    await flushPromises();

    expect(document.activeElement).toBe(nameButton(wrapper).element);
    expect(wrapper.vm.viewer.config).toBeNull();
  });

  test('a role that may only read configs gets no Edit Config', async () => {
    setRole(['configs']);
    phenix.role.policies[0].verbs = ['list', 'get'];
    const wrapper = mountList();
    await flushPromises();
    await view(wrapper);

    const labels = wrapper.findAll('button').map((b) => b.text());
    expect(labels).not.toContain('Edit Config');
    expect(wrapper.find('[data-config-edit="Topology/site"]').exists()).toBe(
      false,
    );
  });
});

describe('ConfigsList Builder control', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  const builderLinkOf = (wrapper) =>
    wrapper.find('[data-config-builder="Topology/site"]');

  // Views the topology, and resolves to the viewer's Builder button.
  async function viewerButton(wrapper) {
    await wrapper.get('[data-config-view="Topology/site"]').trigger('click');
    await flushPromises();
    return wrapper.find('[data-testid="viewer-builder"]');
  }

  test('a role with the Builder gets the link into it', async () => {
    setRole(['*']);
    const wrapper = mountList();
    await flushPromises();

    const link = builderLinkOf(wrapper);
    expect(link.text()).toBe('builder');
    expect(link.attributes('aria-label')).toBe(
      'builder: open Topology site in the Builder',
    );
    expect(wrapper.findComponent(RouterLinkStub).props('to')).toBeTruthy();
    expect((await viewerButton(wrapper)).text()).toBe('Open in Builder');
  });

  test('a role without the Builder gets no link, whatever configs it may read', async () => {
    setRole(['configs']);
    const wrapper = mountList();
    await flushPromises();

    expect(builderLinkOf(wrapper).exists()).toBe(false);
    expect(wrapper.findComponent(RouterLinkStub).exists()).toBe(false);
    // The row keeps its plain tag, and the name still opens the viewer.
    expect(wrapper.get('span.tag').text()).toBe('builder');
    expect((await viewerButton(wrapper)).exists()).toBe(false);
  });
});

describe('ConfigsList focus after the editor closes', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setRole(['*']);
  });

  const editButton = () =>
    document.querySelector('[data-config-edit="Topology/site"]');

  test('the first load focuses the edit button of focusConfig', async () => {
    mountList([], { focusConfig: 'Topology/site' });
    await flushPromises();

    expect(document.activeElement).toBe(editButton());
  });

  test('a refresh after an upload leaves focus where it is', async () => {
    axiosInstance.post.mockResolvedValueOnce({});
    const wrapper = mountList([], { focusConfig: 'Topology/site' });
    await flushPromises();

    await upload(wrapper);
    await flushPromises();

    // The upload fetched the list again.
    expect(axiosInstance.get.mock.calls).toEqual([['configs'], ['configs']]);
    expect(document.activeElement).not.toBe(editButton());
  });
});
