import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import axiosInstance from '@/utils/axios.js';
import { useErrorNotification } from '@/utils/errorNotif';
import ConfigsList from '@/components/configs/ConfigsList.vue';

// The real modules import the Pinia store, the router or Buefy, which need a
// browser. Vitest hoists vi.mock above the imports. The get stub answers the
// config list request that created() makes when the render test runs it.
vi.mock('@/utils/axios.js', () => ({
  default: {
    get: vi.fn(() => Promise.resolve({ data: { configs: [] } })),
    post: vi.fn(),
  },
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));
vi.mock('@/utils/rbac.js', () => ({ roleAllowed: vi.fn() }));

const validation = [
  'nodes[0] "ADServer" (line 14): property "image" is missing (at hardware.drives[0].image)',
  '  hint: "image:" is on line 12 under nodes[0].hardware, but the schema expects it at nodes[0].hardware.drives[0].image',
].join('\n');

const openDialog = { modal: true, title: 'Validation Error', msg: validation };
const closedDialog = { modal: false, title: null, msg: null };

const duplicate = {
  response: {
    status: 400,
    data: { message: 'config with same name already exists' },
  },
};

// Like Buefy's, this b-modal shows its content only while its v-model is true.
const modalStub = {
  props: ['modelValue'],
  render() {
    return this.modelValue ? h('div', this.$slots.default()) : null;
  },
};

// The other Buefy components in the template render nothing here.
const otherBuefyComponents = [
  'b-autocomplete',
  'b-checkbox',
  'b-field',
  'b-icon',
  'b-select',
  'b-switch',
  'b-table',
  'b-table-column',
  'b-tag',
  'b-tooltip',
  'b-upload',
];

// The repo has no DOM test environment or @vue/test-utils, so run the
// component's options against a plain object.
function newConfigsList() {
  const vm = ConfigsList.data();
  for (const [name, method] of Object.entries(ConfigsList.methods)) {
    vm[name] = method.bind(vm);
  }
  return vm;
}

// uploadFile does not return its request promise, so wait for it to settle.
async function upload(vm) {
  vm.uploadFile(new File(['kind: Topology\n'], 'topology.yml'));
  await new Promise((resolve) => setTimeout(resolve));
}

// render server-renders the real template with the given dialog state, which
// needs no DOM, and resolves to the HTML. It adds Vue's warnings to warnings.
function render(error, warnings) {
  const app = createSSRApp({
    ...ConfigsList,
    data: () => ({ ...ConfigsList.data(), error }),
  });
  app.config.warnHandler = (msg) => warnings.push(msg);
  app.component('b-modal', modalStub);
  for (const name of otherBuefyComponents) {
    app.component(name, { render: () => null });
  }
  // A row's Builder tag is a router-link, and the test has no router.
  app.component('router-link', { render: () => null });
  return renderToString(app);
}

describe('ConfigsList upload errors', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('declares a closed validation error dialog', () => {
    expect(ConfigsList.data().error).toEqual(closedDialog);
  });

  test('opens the dialog with the validation lines from a 400', async () => {
    axiosInstance.post.mockRejectedValueOnce({
      response: { status: 400, data: { metadata: { validation } } },
    });

    const vm = newConfigsList();
    await upload(vm);

    expect(axiosInstance.post).toHaveBeenCalledWith(
      'configs',
      expect.any(FormData),
    );
    expect(vm.error).toEqual(openDialog);
    expect(useErrorNotification).not.toHaveBeenCalled();
  });

  test.each([
    ['a 400 without validation metadata', duplicate],
    ['a network error', new Error('Network Error')],
  ])('sends %s to the error notification', async (_, err) => {
    axiosInstance.post.mockRejectedValueOnce(err);

    const vm = newConfigsList();
    await upload(vm);

    expect(useErrorNotification).toHaveBeenCalledWith(err);
    expect(vm.error).toEqual(closedDialog);
  });

  test('resetErrorModal closes and clears the dialog', () => {
    const vm = newConfigsList();
    vm.error = { modal: true, title: 'Validation Error', msg: 'line' };

    vm.resetErrorModal();

    expect(vm.error).toEqual(closedDialog);
  });

  test('renders the dialog with the lines only while it is open', async () => {
    const warnings = [];
    const html = await render(openDialog, warnings);
    const closedHtml = await render(closedDialog, warnings);

    // Every Buefy tag and router-link has a stub, so Vue warned about
    // nothing, not even "Failed to resolve component".
    expect(warnings).toEqual([]);
    // Each render ran created(), which only fetches the config list.
    expect(axiosInstance.get.mock.calls).toEqual([['configs'], ['configs']]);
    expect(axiosInstance.post).not.toHaveBeenCalled();

    expect(html).toContain('>Validation Error</p>');
    expect(html).toContain('>Exit</button>');
    const [, attrs, text] =
      html.match(/<textarea([^>]*)>([^<]*)<\/textarea>/) ?? [];
    expect(attrs).toContain(' readonly');
    expect(attrs).toContain('font-family:monospace');
    // The renderer escapes the quotes around the hostname and the keys.
    expect(text).toBe(validation.replaceAll('"', '&quot;'));

    expect(closedHtml).not.toContain('<textarea');
  });
});
