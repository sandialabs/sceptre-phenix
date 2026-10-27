import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import axiosInstance from '@/utils/axios.js';
import { tagCount } from '@/utils/tagCount';
import VMLabelsModal from '@/components/VMLabelsModal.vue';

// The real modules import the Pinia store, the router or Buefy, which need a
// browser. Vitest hoists vi.mock above the imports.
vi.mock('@/utils/axios.js', () => ({
  default: {
    patch: vi.fn(() => Promise.resolve({ statusText: 'OK' })),
  },
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));
vi.mock('@/utils/rbac', () => ({ roleAllowed: vi.fn(() => true) }));

const topologyNotes = [
  'Domain controller.',
  'Reset the password\nbefore each run.',
];
const vmNote = { key: '__notes_2026-10-27T12:00:00.000Z', value: 'VM note.' };

// Like Buefy's, this b-table renders each column's scoped slot once per row.
const tableStub = {
  props: ['data'],
  render() {
    const columns = (this.$slots.default?.() ?? []).filter(
      (column) => column.children?.default,
    );
    return h(
      'table',
      this.data.map((row, index) =>
        h(
          'tr',
          columns.map((column) =>
            h('td', column.children.default({ row, index })),
          ),
        ),
      ),
    );
  },
};

const stubs = {
  'b-table': tableStub,
  'b-table-column': { render: () => null },
  'b-input': { render: () => h('input') },
  'b-button': {
    props: ['label'],
    render() {
      return h('button', this.label);
    },
  },
  'font-awesome-icon': {
    props: ['icon'],
    render() {
      return h('i', { 'data-icon': this.icon });
    },
  },
};

// render server-renders the real template, which needs no DOM. Server
// rendering skips beforeMount, so the working rows come from the data override.
function render(props, data) {
  const app = createSSRApp(
    { ...VMLabelsModal, data: () => ({ ...VMLabelsModal.data(), ...data }) },
    props,
  );
  app.config.globalProperties.$route = { params: { id: 'exp' } };
  for (const [name, stub] of Object.entries(stubs)) {
    app.component(name, stub);
  }
  return renderToString(app);
}

// newModal runs the component's options against a plain object.
function newModal(props) {
  const vm = {
    ...props,
    ...VMLabelsModal.data(),
    $route: { params: { id: 'exp' } },
    $refs: {},
    $nextTick: () => {},
    $emit: vi.fn(),
  };
  for (const [name, method] of Object.entries(VMLabelsModal.methods)) {
    vm[name] = method.bind(vm);
  }
  return vm;
}

describe('VMLabelsModal topology notes', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('lists the topology notes read-only before the VM notes', async () => {
    const html = await render(
      {
        vmName: 'host-01',
        tags: { [vmNote.key]: vmNote.value },
        topologyNotes,
      },
      { workingNotes: [{ ...vmNote }], workingTags: [] },
    );

    const notes = html.slice(0, html.indexOf('>Labels</p>'));
    const first = notes.indexOf('Domain controller.');
    const second = notes.indexOf('Reset the password\nbefore each run.');
    const table = notes.indexOf('<table');

    expect(first).toBeGreaterThan(-1);
    expect(second).toBeGreaterThan(first);
    expect(table).toBeGreaterThan(second);
    expect(notes.indexOf('>VM note.</textarea>')).toBeGreaterThan(table);
    expect(notes.match(/From the topology/g)).toHaveLength(2);

    // Only the VM note has a text box and a delete control.
    const topology = notes.slice(0, table);
    expect(topology).not.toContain('<textarea');
    expect(topology).not.toContain('data-icon="trash"');
    expect(notes.match(/<textarea/g)).toHaveLength(1);
    expect(notes.match(/data-icon="trash"/g)).toHaveLength(1);
  });

  test('keeps the topology notes out of the editable notes', () => {
    const vm = newModal({
      vmName: 'host-01',
      tags: { role: 'dc', [vmNote.key]: vmNote.value },
      topologyNotes,
    });

    VMLabelsModal.beforeMount.call(vm);

    expect(vm.workingNotes).toEqual([vmNote]);
    expect(vm.workingTags).toEqual([{ key: 'role', value: 'dc' }]);
  });

  test('saves only the VM notes and labels as tags', async () => {
    const vm = newModal({
      vmName: 'host-01',
      tags: { role: 'dc', [vmNote.key]: vmNote.value },
      topologyNotes,
    });
    vm.workingTags = [{ key: 'role', value: 'dc' }];
    vm.workingNotes = [{ key: vmNote.key, value: 'Edited VM note.' }];

    vm.save();
    await new Promise((resolve) => setTimeout(resolve));

    expect(axiosInstance.patch).toHaveBeenCalledWith(
      'experiments/exp/vms/host-01',
      {
        tag_update_mode: 'SET',
        tags: { role: 'dc', [vmNote.key]: 'Edited VM note.' },
      },
    );
    expect(vm.$emit).toHaveBeenCalledWith('close');
  });

  test('sends nothing when only topology notes are shown', () => {
    const vm = newModal({ vmName: 'host-01', tags: {}, topologyNotes });

    vm.save();

    expect(axiosInstance.patch).not.toHaveBeenCalled();
    expect(vm.$emit).toHaveBeenCalledWith('close');
  });

  test('counts the topology notes in the Labels badge', () => {
    const tags = { role: 'dc', [vmNote.key]: vmNote.value, __hidden: 'x' };

    expect(tagCount(tags)).toBe(2);
    expect(tagCount(tags, topologyNotes)).toBe(4);
    expect(tagCount(tags, null)).toBe(2);
  });
});
