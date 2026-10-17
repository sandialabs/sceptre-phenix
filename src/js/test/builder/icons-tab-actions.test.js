// @vitest-environment happy-dom
// The drafts page's Icons tab, mounted and used as a user would: Show, the
// filter, Download, Preview, Upload, the cards' keys, selection and Delete.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';

import BuilderIcons from '@/components/builder/BuilderIcons.vue';

import { builtinIcons } from '@/builder/builtinIcons.js';

import { base64Of, bytesOf, ICON_DATA, png } from './png.js';

const OTHER = base64Of(png(3, 2, [9, 9, 9, 255]));

const ICONS = [
  {
    name: 'hmi',
    owner: 'alice',
    width: 1,
    height: 1,
    bytes: 70,
    aliases: ['old-hmi'],
    data: ICON_DATA,
    canRename: true,
    canDelete: true,
  },
  {
    name: 'valve',
    owner: '',
    width: 3,
    height: 2,
    bytes: 80,
    aliases: [],
    data: OTHER,
    canRename: false,
    canDelete: false,
  },
];

// Two more icons the user may delete, for the selection: pump and tank.
const MORE = ['pump', 'tank'].map((name) => ({
  ...ICONS[0],
  name,
  owner: 'bob',
  aliases: [],
}));

enableAutoUnmount(afterEach);

// The library the tab is given, whose state the calls change as the server
// would. `fails` names the icons remove cannot delete.
function libraryWith({ icons = ICONS, fails = [], ...state } = {}) {
  const library = {
    state: reactive({
      status: 'ready',
      error: '',
      icons: [...icons],
      maxIcons: 2000,
      ...state,
    }),
    load: vi.fn(async () => {}),
    ensure: vi.fn(async () => {}),
    lookup: (name) =>
      library.state.icons.find((icon) =>
        [icon.name, ...(icon.aliases || [])].some(
          (each) => each.toLowerCase() === String(name).toLowerCase(),
        ),
      ) || null,
    upload: vi.fn(async ({ name, data }) => {
      const icon = { ...ICONS[0], name, data, aliases: [] };

      library.state.icons = [...library.state.icons, icon];

      return { icon, created: true };
    }),
    rename: () => new Promise(() => {}),
    remove: vi.fn(async (name) => {
      if (fails.includes(name)) {
        throw new Error(`${name} is in use.`);
      }

      library.state.icons = library.state.icons.filter(
        (icon) => icon.name !== name,
      );
    }),
    failure: (caught) => caught.message,
  };

  return library;
}

function mountTab(props = {}) {
  const library = props.library || libraryWith();
  const wrapper = mount(BuilderIcons, {
    attachTo: document.body,
    props: { ...props, library },
  });

  return { wrapper, library };
}

// The tab with Show on the server's icons.
async function serverTab(props) {
  const opened = mountTab(props);

  await opened.wrapper.get('#icons-tab-show').setValue('server');

  return opened;
}

const button = (wrapper, name) => wrapper.get(`button[aria-label="${name}"]`);
const statusOf = (wrapper) =>
  wrapper.find('[data-testid="icons-tab-status"]').text();
const errorOf = (wrapper) =>
  wrapper.find('[data-testid="icons-tab-error"]').text();
const card = (wrapper, name) =>
  wrapper.get(`[data-testid="icon-card-${name}"]`);
const box = (wrapper, name) =>
  wrapper.get(`[data-testid="icon-card-select-${name}"]`);
const cardsOf = (wrapper, list) =>
  wrapper.findAll(`[data-testid="${list}"] > li`);
const tabStops = (wrapper, list) =>
  cardsOf(wrapper, list)
    .filter((item) => item.attributes('tabindex') === '0')
    .map((item) => item.element);
const previewTitle = (wrapper) =>
  wrapper.find('#icon-preview-title').exists()
    ? wrapper.get('#icon-preview-title').text()
    : null;

describe('the Icons tab, used', () => {
  test('Download saves the PNG as the server stores it, named after the icon', async () => {
    const save = vi.fn();
    const { wrapper } = await serverTab({ save });

    await button(wrapper, 'Download hmi').trigger('click');

    const [[blob, fileName]] = save.mock.calls;

    expect(fileName).toBe('hmi.png');
    expect(blob.type).toBe('image/png');
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(
      bytesOf(ICON_DATA),
    );
    expect(statusOf(wrapper)).toBe('Saved hmi.png.');
  });

  test('Preview opens the preview of its icon, which Close closes', async () => {
    const { wrapper } = await serverTab();

    await button(wrapper, 'Preview hmi').trigger('click');
    expect(previewTitle(wrapper)).toBe('Icon hmi');

    await wrapper.get('[data-testid="icon-preview-close"]').trigger('click');
    expect(previewTitle(wrapper)).toBeNull();
  });

  test('showIcon opens the preview of an icon by any of its names, or says there is none', async () => {
    const { wrapper, library } = mountTab();

    expect(await wrapper.vm.showIcon('OLD-HMI')).toBe(true);
    expect(library.ensure).toHaveBeenCalled();
    expect(previewTitle(wrapper)).toBe('Icon hmi');

    await wrapper.get('[data-testid="icon-preview-close"]').trigger('click');
    expect(await wrapper.vm.showIcon('pump')).toBe(false);
    await flushPromises();
    expect(previewTitle(wrapper)).toBeNull();
    expect(errorOf(wrapper)).toBe('The server has no icon named pump.');
  });

  test('an upload and showIcon show the server icons, where the icon is', async () => {
    const rasterize = vi.fn(async () => ({ name: 'pump', data: ICON_DATA }));
    const { wrapper, library } = mountTab({ rasterize });
    const show = wrapper.get('#icons-tab-show');

    expect(show.element.value).toBe('builtin');
    expect(await wrapper.vm.showIcon('hmi')).toBe(true);
    await flushPromises();
    expect(show.element.value).toBe('server');

    await wrapper.get('[data-testid="icon-preview-close"]').trigger('click');
    await show.setValue('builtin');

    const field = wrapper.get('[data-testid="icons-tab-file"]');
    const file = new File(['<svg/>'], 'pump.svg', { type: 'image/svg+xml' });

    Object.defineProperty(field.element, 'files', {
      configurable: true,
      value: [file],
    });
    await field.trigger('change');
    await flushPromises();

    const name = wrapper.get('[data-testid="icons-tab-upload-name"]');

    expect(rasterize).toHaveBeenCalledWith(file);
    expect(name.element.value).toBe('pump');
    expect(document.activeElement).toBe(name.element);

    await wrapper
      .get('[data-testid="icons-tab-upload-form"]')
      .trigger('submit');
    await flushPromises();

    expect(library.upload).toHaveBeenCalledWith({
      name: 'pump',
      data: ICON_DATA,
    });
    expect(statusOf(wrapper)).toBe('Added pump to the server.');
    expect(show.element.value).toBe('server');
    expect(document.activeElement).toBe(
      button(wrapper, 'Preview pump').element,
    );
  });

  test('Retry reads the server’s icons again', async () => {
    const library = libraryWith({ status: 'failed', error: 'Down.' });
    const { wrapper } = mountTab({ library });

    await wrapper.get('[data-testid="icons-tab-retry"]').trigger('click');
    expect(library.load).toHaveBeenCalledOnce();
  });

  test('lists every built-in icon, which the filter narrows by label or key', async () => {
    const { wrapper } = mountTab();
    const keys = () =>
      cardsOf(wrapper, 'builtin-icons-list').map((item) =>
        item.attributes('data-testid').replace('builtin-icon-card-', ''),
      );
    const countText = () => wrapper.get('#icons-tab-filter-count').text();

    expect(keys()).toEqual(builtinIcons().map((icon) => icon.key));
    await wrapper.get('[data-testid="icons-tab-filter"]').setValue('FIRE');
    expect(keys()).toEqual(['firewall']);
    expect(countText()).toBe(`1 of ${builtinIcons().length} icons shown.`);

    // The server list keeps its own count.
    await wrapper.get('#icons-tab-show').setValue('server');
    expect(countText()).toBe('0 of 2 icons shown.');
    expect(wrapper.get('[data-testid="icons-tab-none-shown"]').text()).toBe(
      'No icon matches the filter.',
    );
  });
});

describe('the Icons tab’s cards, from the keyboard', () => {
  const library = () => libraryWith({ icons: [...ICONS, ...MORE] });

  test('the server cards are one Tab stop that Home and End move, and Space selects the focused card', async () => {
    const { wrapper } = await serverTab({ library: library() });
    const cards = () => cardsOf(wrapper, 'icons-list');

    expect(tabStops(wrapper, 'icons-list')).toEqual([cards()[0].element]);

    cards()[0].element.focus();
    await cards()[0].trigger('keydown', { key: 'End' });
    expect(document.activeElement).toBe(card(wrapper, 'tank').element);
    expect(tabStops(wrapper, 'icons-list')).toEqual([
      card(wrapper, 'tank').element,
    ]);

    await card(wrapper, 'tank').trigger('keydown', { key: ' ' });
    expect(box(wrapper, 'tank').element.checked).toBe(true);
    expect(card(wrapper, 'tank').classes()).toContain('is-selected');
    expect(statusOf(wrapper)).toBe('1 of 3 selected.');

    await card(wrapper, 'tank').trigger('keydown', { key: 'Home' });
    expect(document.activeElement).toBe(card(wrapper, 'hmi').element);

    // The server's own icon cannot be selected: Space there says nothing.
    await card(wrapper, 'valve').trigger('keydown', { key: ' ' });
    expect(wrapper.get('[data-testid="bulk-count-icons"]').text()).toBe(
      '1 of 3 selected',
    );

    await card(wrapper, 'hmi').trigger('keydown', { key: 'Escape' });
    expect(box(wrapper, 'tank').element.checked).toBe(false);
    expect(statusOf(wrapper)).toBe('0 of 3 selected.');
  });

  test('the built-in cards are one Tab stop that the keys move, and nothing in them is selected', async () => {
    const { wrapper } = mountTab();
    const cards = () => cardsOf(wrapper, 'builtin-icons-list');
    const last = cards().at(-1);

    expect(tabStops(wrapper, 'builtin-icons-list')).toEqual([
      cards()[0].element,
    ]);

    cards()[0].element.focus();
    await cards()[0].trigger('keydown', { key: 'End' });
    expect(document.activeElement).toBe(last.element);
    expect(tabStops(wrapper, 'builtin-icons-list')).toEqual([last.element]);

    await last.trigger('keydown', { key: ' ' });
    await last.trigger('keydown', { key: 'Escape' });
    expect(wrapper.find('[data-testid="icons-tab-status"]').exists()).toBe(
      false,
    );

    await last.trigger('keydown', { key: 'Home' });
    expect(document.activeElement).toBe(cards()[0].element);
  });
});

describe('selecting and deleting the Icons tab’s icons', () => {
  const library = (options) =>
    libraryWith({ icons: [...ICONS, ...MORE], ...options });
  const countOf = (wrapper) =>
    wrapper.get('[data-testid="bulk-count-icons"]').text();
  const confirmOf = (wrapper) =>
    wrapper.find('[data-testid="builder-confirm"]');
  const confirmName = (wrapper) =>
    document.getElementById(confirmOf(wrapper).attributes('aria-labelledby'))
      .textContent;

  test('a checkbox selects its card, Shift selects the range, and Select all every card, then none', async () => {
    const { wrapper } = await serverTab({ library: library() });

    await box(wrapper, 'hmi').setValue(true);
    expect(countOf(wrapper)).toBe('1 of 3 selected');
    expect(statusOf(wrapper)).toBe('1 of 3 selected.');
    expect(wrapper.get('#icon-card-state-0').text()).toBe('selected');

    await box(wrapper, 'tank').trigger('click', { shiftKey: true });
    expect(countOf(wrapper)).toBe('3 of 3 selected');
    expect(box(wrapper, 'pump').element.checked).toBe(true);

    const all = wrapper.get('[data-testid="bulk-all-icons"]');

    await all.setValue(false);
    expect(countOf(wrapper)).toBe('0 of 3 selected');
    expect(statusOf(wrapper)).toBe('0 of 3 selected.');
    await all.setValue(true);
    expect(countOf(wrapper)).toBe('3 of 3 selected');
    expect(statusOf(wrapper)).toBe('3 of 3 selected.');
  });

  test('Delete asks first: Cancel keeps the icon, and Delete deletes it and focuses the card in its place', async () => {
    const { wrapper, library: icons } = await serverTab({
      library: library(),
    });
    const remove = button(wrapper, 'Delete hmi from the server');

    remove.element.focus();
    await remove.trigger('click');
    expect(confirmName(wrapper)).toBe('Delete icon?');
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="confirm-cancel"]').element,
    );

    await wrapper.get('[data-testid="confirm-cancel"]').trigger('click');
    expect(confirmOf(wrapper).exists()).toBe(false);
    expect(icons.remove).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(remove.element);

    await remove.trigger('click');
    await wrapper.get('[data-testid="confirm-accept"]').trigger('click');
    await flushPromises();

    expect(icons.remove).toHaveBeenCalledWith('hmi');
    expect(statusOf(wrapper)).toBe('Deleted hmi from the server.');
    expect(wrapper.find('[data-testid="icon-card-hmi"]').exists()).toBe(false);
    expect(document.activeElement).toBe(card(wrapper, 'valve').element);
  });

  test('Delete selected asks once for the batch, deletes each icon, reads the library once, and focuses a card once its row is gone', async () => {
    const { wrapper, library: icons } = await serverTab({
      library: library(),
    });

    await wrapper.get('[data-testid="bulk-all-icons"]').setValue(true);

    // A press focuses the button before its click, as a browser does.
    const remove = wrapper.get('[data-testid="bulk-delete-icons"]');

    remove.element.focus();
    await remove.trigger('click');
    expect(confirmName(wrapper)).toBe('Delete 3 icons?');

    await wrapper.get('[data-testid="confirm-accept"]').trigger('click');
    await flushPromises();

    expect(icons.remove.mock.calls).toEqual([
      ['hmi', { refresh: false }],
      ['pump', { refresh: false }],
      ['tank', { refresh: false }],
    ]);
    expect(icons.load).toHaveBeenCalledOnce();
    expect(statusOf(wrapper)).toBe('Deleted 3 icons from the server.');
    expect(wrapper.find('[data-testid="bulk-bar-icons"]').exists()).toBe(false);
    expect(document.activeElement).toBe(card(wrapper, 'valve').element);
  });

  test('Delete on a card with one icon selected asks as that icon’s Delete does', async () => {
    const { wrapper } = await serverTab({ library: library() });

    await card(wrapper, 'pump').trigger('keydown', { key: 'Delete' });
    expect(confirmOf(wrapper).exists()).toBe(false);

    await card(wrapper, 'pump').trigger('keydown', { key: ' ' });
    await card(wrapper, 'pump').trigger('keydown', { key: 'Delete' });
    expect(confirmName(wrapper)).toBe('Delete icon?');
    expect(confirmOf(wrapper).text()).toContain('Delete pump from the server?');
  });

  test('what a batch left undone is listed, with focus, and Dismiss moves focus to Select all', async () => {
    const { wrapper } = await serverTab({
      library: library({ fails: ['pump'] }),
    });

    await box(wrapper, 'hmi').setValue(true);
    await box(wrapper, 'pump').setValue(true);
    await wrapper.get('[data-testid="bulk-delete-icons"]').trigger('click');
    await wrapper.get('[data-testid="confirm-accept"]').trigger('click');
    await flushPromises();

    const summary = wrapper.get('[data-testid="bulk-summary"]');

    expect(summary.get('h3').text()).toBe(
      '1 of 2 icons could not be deleted. The other 1 was deleted.',
    );
    expect(summary.findAll('li').map((item) => item.text())).toEqual([
      'pump: pump is in use.',
    ]);
    expect(document.activeElement).toBe(summary.element);

    await wrapper.get('[data-testid="bulk-summary-dismiss"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="bulk-summary"]').exists()).toBe(false);
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="bulk-all-icons"]').element,
    );
  });
});
