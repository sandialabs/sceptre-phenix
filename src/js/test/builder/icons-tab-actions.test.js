// @vitest-environment happy-dom
// The drafts page's Icons tab, mounted and used as a user would: Show, the
// filter, Download, View, Upload, the cards' keys, selection, Download
// selected and Delete.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';

const save = vi.hoisted(() => vi.fn());

vi.mock('file-saver', () => ({ saveAs: save }));

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
// would. `fails` names the icons remove cannot delete. `gone` names the
// icons the server no longer has: remove drops them and fails, as on a 404.
function libraryWith({ icons = ICONS, fails = [], gone = [], ...state } = {}) {
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
    ensureImages: vi.fn(async () => {}),
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
    remove: vi.fn(async ({ name }) => {
      if (fails.includes(name)) {
        throw new Error(`${name} is in use.`);
      }

      library.state.icons = library.state.icons.filter(
        (icon) => icon.name !== name,
      );

      if (gone.includes(name)) {
        throw new Error(`The server has no icon ${name}.`);
      }
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
  // The library lists icons without their images, and reads each image
  // when it is drawn.
  test('a card draws an empty square until the image of its icon is read, then the image', async () => {
    const images = reactive(new Map());
    const library = libraryWith();

    library.lookup = (name) => {
      const icon = library.state.icons.find((entry) => entry.name === name);
      const data = images.get(name);

      return icon && { ...icon, data, pending: data === undefined };
    };

    const { wrapper } = await serverTab({ library });
    const src = () =>
      card(wrapper, 'hmi').get('h2 img').attributes('src') || '';

    expect(src()).toMatch(/^data:image\/png;base64,/);
    expect(src()).not.toContain(ICON_DATA);

    images.set('hmi', ICON_DATA);
    await flushPromises();

    expect(src()).toBe(`data:image/png;base64,${ICON_DATA}`);
  });

  test('Download saves the PNG as the server stores it, named after the icon, once its image is read', async () => {
    save.mockClear();
    const { wrapper, library } = await serverTab();

    await button(wrapper, 'Download hmi').trigger('click');
    await flushPromises();

    expect(library.ensureImages).toHaveBeenCalledExactlyOnceWith(['hmi']);

    const [[blob, fileName]] = save.mock.calls;

    expect(fileName).toBe('hmi.png');
    expect(blob.type).toBe('image/png');
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(
      bytesOf(ICON_DATA),
    );
    expect(statusOf(wrapper)).toBe('Saved hmi.png.');
  });

  test('View opens the preview of its icon, which Close closes', async () => {
    const { wrapper } = await serverTab();

    await button(wrapper, 'View hmi').trigger('click');
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
    expect(document.activeElement).toBe(button(wrapper, 'View pump').element);
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
    expect(statusOf(wrapper)).toBe('1 of 4 selected.');

    await card(wrapper, 'tank').trigger('keydown', { key: 'Home' });
    expect(document.activeElement).toBe(card(wrapper, 'hmi').element);

    // The server's own icon can be selected too, for Download selected.
    await card(wrapper, 'valve').trigger('keydown', { key: ' ' });
    expect(wrapper.get('[data-testid="bulk-count-icons"]').text()).toBe(
      '2 of 4 selected',
    );
    expect(statusOf(wrapper)).toBe('2 of 4 selected.');

    await card(wrapper, 'hmi').trigger('keydown', { key: 'Escape' });
    expect(box(wrapper, 'tank').element.checked).toBe(false);
    expect(statusOf(wrapper)).toBe('0 of 4 selected.');
  });

  test('the built-in cards are one Tab stop that the keys move, and the keys select them as they do the server cards', async () => {
    const { wrapper } = mountTab();
    const cards = () => cardsOf(wrapper, 'builtin-icons-list');
    const last = cards().at(-1);
    const total = builtinIcons().length;

    expect(tabStops(wrapper, 'builtin-icons-list')).toEqual([
      cards()[0].element,
    ]);

    cards()[0].element.focus();
    await cards()[0].trigger('keydown', { key: 'End' });
    expect(document.activeElement).toBe(last.element);
    expect(tabStops(wrapper, 'builtin-icons-list')).toEqual([last.element]);

    await last.trigger('keydown', { key: ' ' });
    expect(last.classes()).toContain('is-selected');
    expect(statusOf(wrapper)).toBe(`1 of ${total} selected.`);
    await last.trigger('keydown', { key: 'ArrowLeft', shiftKey: true });
    expect(statusOf(wrapper)).toBe(`2 of ${total} selected.`);
    await last.trigger('keydown', { key: 'a', ctrlKey: true });
    expect(statusOf(wrapper)).toBe(`${total} of ${total} selected.`);
    await last.trigger('keydown', { key: 'Escape' });
    expect(statusOf(wrapper)).toBe(`0 of ${total} selected.`);

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
    expect(countOf(wrapper)).toBe('1 of 4 selected');
    expect(statusOf(wrapper)).toBe('1 of 4 selected.');
    expect(wrapper.get('#icon-card-state-0').text()).toBe('selected');

    await box(wrapper, 'tank').trigger('click', { shiftKey: true });
    expect(countOf(wrapper)).toBe('4 of 4 selected');
    expect(box(wrapper, 'pump').element.checked).toBe(true);
    expect(box(wrapper, 'valve').element.checked).toBe(true);

    const all = wrapper.get('[data-testid="bulk-all-icons"]');

    await all.setValue(false);
    expect(countOf(wrapper)).toBe('0 of 4 selected');
    expect(statusOf(wrapper)).toBe('0 of 4 selected.');
    await all.setValue(true);
    expect(countOf(wrapper)).toBe('4 of 4 selected');
    expect(statusOf(wrapper)).toBe('4 of 4 selected.');
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

    expect(icons.remove).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'hmi' }),
    );
    expect(statusOf(wrapper)).toBe('Deleted hmi from the server.');
    expect(wrapper.find('[data-testid="icon-card-hmi"]').exists()).toBe(false);
    expect(document.activeElement).toBe(card(wrapper, 'valve').element);
  });

  test('Delete selected asks once for the batch, deletes each icon, reads the library once, and keeps focus', async () => {
    const { wrapper, library: icons } = await serverTab({
      library: library(),
    });

    for (const name of ['hmi', 'pump', 'tank']) {
      await box(wrapper, name).setValue(true);
    }

    // A press focuses the button before its click, as a browser does.
    const remove = wrapper.get('[data-testid="bulk-delete-icons"]');

    remove.element.focus();
    await remove.trigger('click');
    expect(confirmName(wrapper)).toBe('Delete 3 icons?');

    await wrapper.get('[data-testid="confirm-accept"]').trigger('click');
    await flushPromises();

    expect(icons.remove.mock.calls.map(([icon]) => icon.name)).toEqual([
      'hmi',
      'pump',
      'tank',
    ]);
    // Each delete takes the server's answer into the list: none reads it.
    expect(icons.load).not.toHaveBeenCalled();
    expect(statusOf(wrapper)).toBe('Deleted 3 icons from the server.');
    expect(countOf(wrapper)).toBe('0 of 1 selected');
    // The row stays for the icon left, so Delete selected keeps focus.
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="bulk-delete-icons"]').element,
    );
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
  test('a batch that empties the list still lists what it left undone, and Dismiss moves focus to Upload icon…', async () => {
    const { wrapper } = await serverTab({
      library: libraryWith({ icons: MORE, gone: ['pump'] }),
    });

    await wrapper.get('[data-testid="bulk-all-icons"]').setValue(true);
    await wrapper.get('[data-testid="bulk-delete-icons"]').trigger('click');
    await wrapper.get('[data-testid="confirm-accept"]').trigger('click');
    await flushPromises();

    const summary = wrapper.get('[data-testid="bulk-summary"]');

    expect(wrapper.find('[data-testid="icons-tab-empty"]').exists()).toBe(true);
    expect(summary.get('h3').text()).toBe(
      '1 of 2 icons could not be deleted. The other 1 was deleted.',
    );
    expect(summary.findAll('li').map((item) => item.text())).toEqual([
      'pump: The server has no icon pump.',
    ]);
    expect(document.activeElement).toBe(summary.element);

    await wrapper.get('[data-testid="bulk-summary-dismiss"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="bulk-summary"]').exists()).toBe(false);
    expect(
      wrapper
        .get('.builder-icons-tab__actions')
        .element.contains(document.activeElement),
    ).toBe(true);
  });
});

describe('the Icons tab’s PNG files and the built-in icons in a batch', () => {
  const PNG = base64Of(png(2, 2, [27, 35, 48, 255]));
  // A rasterize that draws every SVG as PNG, as rasterizeIcon would.
  const drawing = () => vi.fn(async () => ({ name: '', data: PNG }));
  const library = (options) =>
    libraryWith({ icons: [...ICONS, ...MORE], ...options });
  const builtinBox = (wrapper, key) =>
    wrapper.get(`[data-testid="builtin-icon-select-${key}"]`);
  const summaryOf = (wrapper) => wrapper.get('[data-testid="bulk-summary"]');

  test('Download of a built-in icon saves its SVG drawn as a 96 by 96 PNG file', async () => {
    save.mockClear();
    const rasterize = drawing();
    const { wrapper } = mountTab({ rasterize });

    await button(wrapper, 'Download Firewall').trigger('click');
    await flushPromises();

    const [[svg]] = rasterize.mock.calls;

    expect(svg.type).toBe('image/svg+xml');
    expect(await svg.text()).toContain('width="96" height="96"');

    const [[blob, fileName]] = save.mock.calls;

    expect(fileName).toBe('firewall.png');
    expect(blob.type).toBe('image/png');
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(bytesOf(PNG));
    expect(statusOf(wrapper)).toBe('Saved firewall.png.');
  });

  test('a built-in icon the browser cannot draw is not saved, and the error says so', async () => {
    save.mockClear();
    const rasterize = vi.fn(async () => {
      throw new Error('no canvas');
    });
    const { wrapper } = mountTab({ rasterize });

    await button(wrapper, 'Download Firewall').trigger('click');
    await flushPromises();

    expect(save).not.toHaveBeenCalled();
    expect(errorOf(wrapper)).toBe(
      'Could not save Firewall: the Builder could not draw it as a PNG file.',
    );
  });

  test('Download selected saves one PNG file for each selected icon, built-in icons too', async () => {
    save.mockClear();
    const { wrapper } = mountTab({ rasterize: drawing() });

    await builtinBox(wrapper, 'router').setValue(true);
    await builtinBox(wrapper, 'firewall').setValue(true);
    await wrapper.get('[data-testid="bulk-download-icons"]').trigger('click');
    await flushPromises();

    expect(save.mock.calls.map(([, fileName]) => fileName)).toEqual([
      'router.png',
      'firewall.png',
    ]);
    expect(statusOf(wrapper)).toBe('Downloaded 2 icons.');

    save.mockClear();
    await wrapper.get('#icons-tab-show').setValue('server');
    await box(wrapper, 'hmi').setValue(true);
    await box(wrapper, 'valve').setValue(true);
    await wrapper.get('[data-testid="bulk-download-icons"]').trigger('click');
    await flushPromises();

    expect(save.mock.calls.map(([, fileName]) => fileName)).toEqual([
      'hmi.png',
      'valve.png',
    ]);
    expect(statusOf(wrapper)).toBe('Downloaded 2 icons.');
  });

  test('Download selected turns while it runs, with progress on the row', async () => {
    let draw;
    const rasterize = vi.fn(
      () =>
        new Promise((resolve) => {
          draw = () => resolve({ name: '', data: PNG });
        }),
    );
    const { wrapper } = mountTab({ rasterize });
    const download = wrapper.get('[data-testid="bulk-download-icons"]');

    await builtinBox(wrapper, 'router').setValue(true);
    await download.trigger('click');
    await flushPromises();

    expect(download.attributes('aria-busy')).toBe('true');
    expect(wrapper.get('[data-testid="bulk-progress-icons"]').text()).toBe(
      'Downloading 0 of 1…',
    );
    expect(
      wrapper.get('[data-testid="bulk-delete-icons"]').attributes('aria-busy'),
    ).toBeUndefined();

    draw();
    await flushPromises();
    expect(download.attributes('aria-busy')).toBeUndefined();
    expect(statusOf(wrapper)).toBe('Downloaded 1 icon.');
  });

  test('Download selected lists the icons it could not save, with why', async () => {
    save.mockClear();
    const icons = library();

    icons.lookup = (name) =>
      name === 'pump'
        ? { ...MORE[0], data: undefined }
        : icons.state.icons.find((icon) => icon.name === name) || null;

    const { wrapper } = await serverTab({ library: icons });

    await box(wrapper, 'hmi').setValue(true);
    await box(wrapper, 'pump').setValue(true);
    await wrapper.get('[data-testid="bulk-download-icons"]').trigger('click');
    await flushPromises();

    expect(save.mock.calls.map(([, fileName]) => fileName)).toEqual([
      'hmi.png',
    ]);
    expect(summaryOf(wrapper).get('h3').text()).toBe(
      '1 of 2 icons could not be downloaded. The other 1 was downloaded.',
    );
    expect(
      summaryOf(wrapper)
        .findAll('li')
        .map((item) => item.text()),
    ).toEqual(['pump: The Builder could not read its image.']);
    expect(document.activeElement).toBe(summaryOf(wrapper).element);
  });

  test('Delete selected on built-in icons deletes nothing and says why, without a question', async () => {
    const { wrapper, library: icons } = mountTab({ library: library() });

    await builtinBox(wrapper, 'router').setValue(true);
    await builtinBox(wrapper, 'firewall').setValue(true);
    await wrapper.get('[data-testid="bulk-delete-icons"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="builder-confirm"]').exists()).toBe(
      false,
    );
    expect(icons.remove).not.toHaveBeenCalled();
    expect(summaryOf(wrapper).get('h3').text()).toBe(
      '2 of 2 icons could not be deleted.',
    );
    expect(
      summaryOf(wrapper)
        .findAll('li')
        .map((item) => item.text()),
    ).toEqual([
      'Router: You cannot delete a built-in icon.',
      'Firewall: You cannot delete a built-in icon.',
    ]);
    expect(document.activeElement).toBe(summaryOf(wrapper).element);

    // The Delete key on a card does the same.
    await wrapper.get('[data-testid="bulk-summary-dismiss"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="bulk-summary"]').exists()).toBe(false);
    await wrapper
      .get('[data-testid="builtin-icon-card-router"]')
      .trigger('keydown', { key: 'Delete' });
    await flushPromises();
    expect(summaryOf(wrapper).get('h3').text()).toBe(
      '2 of 2 icons could not be deleted.',
    );
    expect(icons.remove).not.toHaveBeenCalled();
  });

  test('Delete selected deletes the uploaded icons and lists the icons it cannot delete', async () => {
    const { wrapper, library: icons } = await serverTab({
      library: library(),
    });

    await box(wrapper, 'hmi').setValue(true);
    await box(wrapper, 'valve').setValue(true);
    await box(wrapper, 'pump').setValue(true);
    await wrapper.get('[data-testid="bulk-delete-icons"]').trigger('click');

    const confirm = wrapper.get('[data-testid="builder-confirm"]');

    expect(confirm.text()).toContain('Delete 2 icons?');
    expect(confirm.text()).toContain(
      'You cannot delete 1 other selected icon.',
    );

    await wrapper.get('[data-testid="confirm-accept"]').trigger('click');
    await flushPromises();

    expect(icons.remove.mock.calls.map(([icon]) => icon.name)).toEqual([
      'hmi',
      'pump',
    ]);
    expect(summaryOf(wrapper).get('h3').text()).toBe(
      '1 of 3 icons could not be deleted. The other 2 were deleted.',
    );
    expect(
      summaryOf(wrapper)
        .findAll('li')
        .map((item) => item.text()),
    ).toEqual([
      "valve: You cannot delete an icon from the server's template files.",
    ]);
  });

  test('another Show clears the selection', async () => {
    const { wrapper } = mountTab({ library: library() });
    const total = builtinIcons().length;

    await builtinBox(wrapper, 'router').setValue(true);
    expect(wrapper.get('[data-testid="bulk-count-icons"]').text()).toBe(
      `1 of ${total} selected`,
    );

    await wrapper.get('#icons-tab-show').setValue('server');
    expect(wrapper.get('[data-testid="bulk-count-icons"]').text()).toBe(
      '0 of 4 selected',
    );

    await wrapper.get('#icons-tab-show').setValue('builtin');
    expect(builtinBox(wrapper, 'router').element.checked).toBe(false);
  });

  test('another Show removes the summary of the last batch', async () => {
    const { wrapper } = mountTab({ library: library() });

    await builtinBox(wrapper, 'router').setValue(true);
    await wrapper.get('[data-testid="bulk-delete-icons"]').trigger('click');
    await flushPromises();
    expect(summaryOf(wrapper).get('h3').text()).toBe(
      '1 of 1 icon could not be deleted.',
    );

    await wrapper.get('#icons-tab-show').setValue('server');
    expect(wrapper.find('[data-testid="bulk-summary"]').exists()).toBe(false);

    await wrapper.get('#icons-tab-show').setValue('builtin');
    expect(wrapper.find('[data-testid="bulk-summary"]').exists()).toBe(false);
  });
});
