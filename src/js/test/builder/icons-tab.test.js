// The drafts page's Icons tab and its preview dialog, rendered on the server,
// and the download of an icon as the PNG file the server stores.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import BuilderIcons from '@/components/builder/BuilderIcons.vue';
import IconPreviewDialog from '@/components/builder/dialogs/IconPreviewDialog.vue';

import { builtinIcons } from '@/builder/builtinIcons.js';
import { saveIconFile } from '@/builder/icons.js';

import { tags, textOf } from './fixtures.js';
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

// The library the tab is given: its state as the server last listed it,
// and calls that never answer, except ensure.
function libraryWith(state = {}) {
  const pending = () => new Promise(() => {});
  const icons = state.icons || [];

  return {
    state: {
      status: 'ready',
      error: '',
      icons,
      maxIcons: 2000,
      ...state,
    },
    load: pending,
    ensure: vi.fn(async () => {}),
    lookup: (name) =>
      icons.find((icon) =>
        [icon.name, ...(icon.aliases || [])].some(
          (each) => each.toLowerCase() === String(name).toLowerCase(),
        ),
      ) || null,
    upload: pending,
    rename: pending,
    remove: pending,
    failure: () => 'failed',
  };
}

// Renders a component and returns its HTML and setup bindings; `state` sets
// bindings, such as the list Show shows, before it renders.
async function render(component, props = {}, state = {}) {
  const app = createSSRApp({ render: () => h(component, props) });
  let bindings = null;

  app.mixin({
    created() {
      if (this.$.type === component) {
        bindings = this.$.setupState;
        Object.assign(bindings, state);
      }
    },
  });

  return { html: await renderToString(app), bindings };
}

// The card of an icon, as HTML.
function cardOf(html, name) {
  const at = html.indexOf(`data-testid="icon-card-${name}"`);

  expect(at, name).toBeGreaterThan(-1);

  return html.slice(html.lastIndexOf('<li', at), html.indexOf('</li>', at));
}

// The tab with Show on the server's icons.
function renderServer(props) {
  return render(BuilderIcons, props, { showing: 'server' });
}

describe('the Icons tab', () => {
  test('has a card for each icon: its name, other names, who uploaded it and size', async () => {
    const { html } = await renderServer({
      library: libraryWith({ icons: ICONS }),
    });
    const hmi = cardOf(html, 'hmi');
    const valve = cardOf(html, 'valve');

    expect(textOf(hmi)).toBe(
      'hmi Select hmi Also named old-hmi Uploaded by alice 1 × 1 pixels, 0.1 KiB Preview Download Delete not selected',
    );
    // An icon the server added has no uploader: it is the server's.
    expect(textOf(valve)).toBe(
      'valve Server 3 × 2 pixels, 0.1 KiB Preview Download',
    );
    // The icon is decoration: the name says which it is.
    const [image] = tags(hmi, 'img');

    expect(image).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    expect(image).toContain('aria-hidden="true"');
    // Every button names its icon.
    expect(hmi).toContain('aria-label="Preview hmi"');
    expect(hmi).toContain('aria-label="Download hmi"');
    expect(hmi).toContain('aria-label="Delete hmi from the server"');
    // The cards are one Tab stop, named by the icon and whether it is
    // selected when it can be.
    expect(tags(html, 'li')[0]).toContain('tabindex="0"');
    expect(tags(html, 'li')[0]).toContain(
      'aria-labelledby="icon-card-name-0 icon-card-state-0"',
    );
    expect(tags(html, 'li')[1]).toContain('tabindex="-1"');
    expect(tags(html, 'li')[1]).toContain('aria-labelledby="icon-card-name-1"');
  });

  test('has Delete, a checkbox and Delete selected on every icon but the server’s, whoever uploaded it', async () => {
    const bobs = { ...ICONS[0], name: 'pump', owner: 'bob', aliases: [] };
    const { html } = await renderServer({
      library: libraryWith({ icons: [...ICONS, bobs] }),
    });

    for (const name of ['hmi', 'pump']) {
      expect(cardOf(html, name)).toContain(`data-testid="icon-delete-${name}"`);
      expect(cardOf(html, name)).toContain(
        `data-testid="icon-card-select-${name}"`,
      );
    }
    expect(textOf(cardOf(html, 'pump'))).toContain('Uploaded by bob');
    expect(cardOf(html, 'valve')).not.toContain('icon-delete-');
    expect(cardOf(html, 'valve')).not.toContain('type="checkbox"');
    expect(html).toContain('data-testid="bulk-delete-icons"');
    expect(html).toContain('aria-label="Bulk actions: Icons"');
    // Why some icons have no Delete.
    expect(textOf(html)).toMatch(
      /Icons from the server(’|')s template files cannot be deleted\./,
    );
    expect(textOf(html)).not.toContain('builder-icons');

    const mine = await renderServer({
      library: libraryWith({ icons: [ICONS[0]] }),
    });

    expect(mine.html).not.toContain('icons-tab-delete-note');
    const none = await renderServer({
      library: libraryWith({ icons: [ICONS[1]] }),
    });

    expect(none.html).not.toContain('bulk-delete-icons');
  });

  test('has Upload icon… and the usage', async () => {
    const { html } = await render(BuilderIcons, {
      library: libraryWith({ icons: ICONS }),
    });
    const [upload] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="icons-tab-upload"'),
    );

    expect(upload).toBeTruthy();
    expect(html).toContain('data-testid="icons-tab-file"');
    expect(textOf(html)).toContain('The library holds 2 of 2000 icons.');
    // The status and error regions are there, empty, from the start.
    expect(html).toMatch(
      /<p class="builder-dialog__message"[^>]*role="status"[^>]*><!----><\/p>/,
    );
    expect(html).toMatch(
      /<p class="builder-dialog__message builder-dialog__error"[^>]*role="alert"[^>]*><!----><\/p>/,
    );
  });

  test('says when it loads, when it failed with Retry, and when the server has no icons', async () => {
    const loading = await renderServer({
      library: libraryWith({ status: 'loading' }),
    });
    const failed = await renderServer({
      library: libraryWith({ status: 'failed', error: 'Server unreachable.' }),
    });
    const empty = await renderServer({ library: libraryWith() });

    expect(tags(loading.html, 'p').join()).toContain('role="status"');
    expect(textOf(loading.html)).toContain('Loading…');
    expect(textOf(failed.html)).toContain(
      "Could not load the server's icons. Server unreachable.",
    );
    expect(failed.html).toContain('data-testid="icons-tab-retry"');
    expect(textOf(empty.html)).toContain('The server has no icons yet.');
    for (const { html } of [loading, failed, empty]) {
      expect(html).not.toContain('data-testid="icons-list"');
    }
  });

  test('says that it could not load the server icons, with Retry, while it shows the built-in icons', async () => {
    const failed = await render(BuilderIcons, {
      library: libraryWith({ status: 'failed', error: 'Server unreachable.' }),
    });
    const loading = await render(BuilderIcons, {
      library: libraryWith({ status: 'loading' }),
    });

    expect(textOf(failed.html)).toContain(
      "Could not load the server's icons. Server unreachable.",
    );
    expect(failed.html).toContain('data-testid="icons-tab-retry"');
    expect(failed.html).toContain('data-testid="builtin-icons-list"');
    // Loading is of the server's icons only.
    expect(loading.html).not.toContain('data-testid="icons-tab-loading"');
  });

  test('Download saves the PNG as the server stores it, named after the icon', async () => {
    const save = vi.fn();
    const { bindings } = await render(BuilderIcons, {
      library: libraryWith({ icons: ICONS }),
      save,
    });

    bindings.download(ICONS[0]);

    const [[blob, fileName]] = save.mock.calls;

    expect(fileName).toBe('hmi.png');
    expect(blob.type).toBe('image/png');
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(
      bytesOf(ICON_DATA),
    );
    expect(bindings.status.text).toBe('Saved hmi.png.');
  });

  test('showIcon opens the preview of an icon by any of its names, or says there is none', async () => {
    const library = libraryWith({ icons: ICONS });
    const { bindings } = await render(BuilderIcons, { library });

    expect(await bindings.showIcon('OLD-HMI')).toBe(true);
    expect(library.ensure).toHaveBeenCalled();
    expect(bindings.previewing.name).toBe('hmi');

    bindings.previewing = null;
    expect(await bindings.showIcon('pump')).toBe(false);
    expect(bindings.previewing).toBeNull();
    expect(bindings.error.text).toBe('The server has no icon named pump.');
  });
});

describe('the built-in icons in the Icons tab', () => {
  test('Show offers the built-in icons, at first, and the server icons', async () => {
    const { html } = await render(BuilderIcons, {
      library: libraryWith({ icons: ICONS }),
    });
    const show = html.slice(
      html.indexOf('<select id="icons-tab-show"'),
      html.indexOf('</select>'),
    );

    expect(html).toMatch(/<label for="icons-tab-show"[^>]*>Show<\/label>/);
    expect(textOf(show)).toBe('Built-in icons Server icons');
    expect(html).toContain('data-testid="builtin-icons-list"');
    expect(html).not.toContain('data-testid="icons-list"');

    // The server's icons show when Show chooses them.
    const server = await renderServer({
      library: libraryWith({ icons: ICONS }),
    });

    expect(server.html).toContain('data-testid="icons-list"');
    expect(server.html).not.toContain('builtin-icons-list');
  });

  test('an upload and showIcon show the server icons, where the icon is', async () => {
    const { bindings } = await render(BuilderIcons, {
      library: libraryWith({ icons: ICONS }),
    });

    expect(bindings.showing).toBe('builtin');
    expect(await bindings.showIcon('hmi')).toBe(true);
    expect(bindings.showing).toBe('server');

    bindings.showing = 'builtin';
    await bindings.onUploaded({ icon: ICONS[0] });
    expect(bindings.showing).toBe('server');
  });

  test('lists every built-in icon, which the filter narrows by label or key', async () => {
    const { bindings } = await render(BuilderIcons, {
      library: libraryWith({ icons: ICONS }),
    });

    expect(bindings.builtinShown).toEqual(builtinIcons());
    bindings.filterText = 'FIRE';
    expect(bindings.builtinShown.map((icon) => icon.key)).toEqual(['firewall']);
    expect(bindings.filterCount).toBe(
      `1 of ${builtinIcons().length} icons shown.`,
    );
    // The server list keeps its own count.
    bindings.showing = 'server';
    expect(bindings.filterCount).toBe('0 of 2 icons shown.');
  });
});

describe('the icon preview', () => {
  test('shows a built-in icon large and at the three node sizes, with its label, key and owner', async () => {
    const { html } = await render(IconPreviewDialog, {
      icon: { builtin: true, key: 'firewall', label: 'Firewall' },
    });
    const svgs = tags(html, 'svg').filter((tag) =>
      tag.includes('builder-icon--firewall'),
    );

    expect(html).toMatch(
      /<h2 id="icon-preview-title"[^>]*>Icon Firewall<\/h2>/,
    );
    expect(svgs[0]).toContain('width="96"');
    expect(svgs[0]).toContain('aria-label="Firewall at 96 pixels"');
    expect(svgs[0]).toContain('data-testid="icon-preview-image"');
    expect(textOf(html)).toContain('Shown at 96 × 96 pixels');
    expect(
      svgs.slice(1, 4).map((tag) => tag.match(/width="(\d+)"/)[1]),
    ).toEqual(['16', '24', '32']);
    expect(svgs).toHaveLength(4);
    expect(html).not.toContain('<img');
    expect(
      textOf(
        html.slice(
          html.lastIndexOf('<dl', html.indexOf('icon-preview-details')),
        ),
      ),
    ).toMatch(
      /^Name Firewall Key firewall Source Built-in File SVG Download Close/,
    );
    expect(html).toContain('aria-label="Download Firewall"');
  });

  test('shows the icon at its stored size, sharp, and at the three node sizes', async () => {
    const { html } = await render(IconPreviewDialog, {
      icon: { ...ICONS[1], aliases: ['tap', 'faucet'], owner: 'bob' },
    });
    const [stored] = tags(html, 'img').filter((tag) =>
      tag.includes('data-testid="icon-preview-image"'),
    );

    expect(html).toMatch(/<h2 id="icon-preview-title"[^>]*>Icon valve<\/h2>/);
    expect(stored).toContain('class="icon-preview__pixels"');
    expect(stored).toContain('width="3"');
    expect(stored).toContain('height="2"');
    expect(stored).toContain('alt="valve at its stored size"');
    expect(textOf(html)).toContain('Stored size: 3 × 2 pixels');

    const sizes = html.slice(
      html.indexOf('data-testid="icon-preview-sizes"'),
      html.indexOf('</ul>'),
    );

    expect(
      tags(sizes, 'img').map((tag) => tag.match(/width="(\d+)"/)[1]),
    ).toEqual(['16', '24', '32']);
    expect(textOf(sizes)).toContain('16 pixels 24 pixels 32 pixels');
    expect(
      textOf(
        html.slice(
          html.lastIndexOf('<dl', html.indexOf('icon-preview-details')),
        ),
      ),
    ).toMatch(
      /^Name valve Other names tap, faucet Source Uploaded by bob Size 3 × 2 pixels, 80 bytes Download Close/,
    );
    expect(html).toContain('aria-label="Download valve"');
  });

  test('shows the messages of the tab, because the tab is inert behind it', async () => {
    const quiet = await render(IconPreviewDialog, { icon: ICONS[1] });
    const { html } = await render(IconPreviewDialog, {
      icon: ICONS[1],
      status: { text: 'Saved valve.png.', key: 1 },
      error: { text: 'valve cannot be saved.', key: 1 },
    });

    expect(tags(quiet.html, 'p').join()).toContain('role="status"');
    expect(tags(quiet.html, 'p').join()).toContain('role="alert"');
    expect(quiet.html).not.toContain('icon-preview-status');
    expect(html).toMatch(
      /data-testid="icon-preview-status"[^>]*>Saved valve\.png\.</,
    );
    expect(html).toMatch(
      /data-testid="icon-preview-error"[^>]*>valve cannot be saved\.</,
    );
  });

  test('an icon the server added is the server’s', async () => {
    const { html } = await render(IconPreviewDialog, { icon: ICONS[1] });

    expect(textOf(html)).toContain('Source Server');
    expect(textOf(html)).not.toContain('Other names');
  });
});

describe('saveIconFile', () => {
  test('saves the decoded bytes, not a new drawing, and nothing for data that is not an icon', async () => {
    const saveAs = vi.fn();

    expect(saveIconFile({ name: 'plc.v2', data: ICON_DATA }, { saveAs })).toBe(
      'plc.v2.png',
    );
    expect(new Uint8Array(await saveAs.mock.calls[0][0].arrayBuffer())).toEqual(
      bytesOf(ICON_DATA),
    );
    expect(saveIconFile({ name: 'bad', data: 'not base64!' }, { saveAs })).toBe(
      '',
    );
    expect(saveIconFile({ name: '..', data: ICON_DATA }, { saveAs })).toBe('');
    expect(saveAs).toHaveBeenCalledTimes(1);
  });
});
