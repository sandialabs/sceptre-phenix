// The drafts page's Icons tab and its preview dialog, rendered on the server,
// and saveIconFile. icons-tab-actions.test.js uses the tab.

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

// Renders a component and returns its HTML. `state` sets bindings, such as
// the list Show shows, before it renders.
async function render(component, props = {}, state = {}) {
  const app = createSSRApp({ render: () => h(component, props) });

  app.mixin({
    created() {
      if (this.$.type === component) {
        Object.assign(this.$.setupState, state);
      }
    },
  });

  return { html: await renderToString(app) };
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
      'hmi Select hmi Also named old-hmi Uploaded by alice 1 × 1 pixels, 0.1 KiB View Download Delete not selected',
    );
    // An icon the server added has no uploader: it is the server's.
    expect(textOf(valve)).toBe(
      'valve Select valve Server 3 × 2 pixels, 0.1 KiB View Download not selected',
    );
    // The icon is decoration: the name says which it is.
    const [image] = tags(hmi, 'img');

    expect(image).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    expect(image).toContain('aria-hidden="true"');
    // Every button names its icon.
    expect(hmi).toContain('aria-label="View hmi"');
    expect(hmi).toContain('aria-label="Download hmi"');
    expect(hmi).toContain('aria-label="Delete hmi from the server"');
    // The item actions: View with the eye, Download with the down arrow,
    // and Delete red with the trash can.
    const buttons = tags(hmi, 'button');

    expect(buttons[0]).toContain('builder-action-button--view');
    expect(buttons[1]).toContain('builder-action-button--download');
    expect(buttons[2]).toContain('builder-action-button--delete');
    expect(buttons[2]).toContain('builder-button--danger');
    for (const icon of ['eye', 'download', 'trash']) {
      expect(hmi).toContain(`builder-icon--${icon}`);
    }
    // The cards are one Tab stop, named by the icon and whether it is
    // selected when it can be.
    expect(tags(html, 'li')[0]).toContain('tabindex="0"');
    expect(tags(html, 'li')[0]).toContain(
      'aria-labelledby="icon-card-name-0 icon-card-state-0"',
    );
    expect(tags(html, 'li')[1]).toContain('tabindex="-1"');
    expect(tags(html, 'li')[1]).toContain(
      'aria-labelledby="icon-card-name-1 icon-card-state-1"',
    );
  });

  test('has a checkbox on every icon, and Delete on every icon but the server’s, whoever uploaded it', async () => {
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
    expect(cardOf(html, 'valve')).toContain(
      'data-testid="icon-card-select-valve"',
    );
    expect(html).toContain('data-testid="bulk-download-icons"');
    expect(html).toContain('data-testid="bulk-delete-icons"');
    expect(html).toContain('aria-label="Bulk actions: Icons"');
    // Why some icons have no Delete.
    expect(textOf(html)).toMatch(
      /You cannot delete icons from the server(’|')s template files\./,
    );
    expect(textOf(html)).not.toContain('builder-icons');

    const mine = await renderServer({
      library: libraryWith({ icons: [ICONS[0]] }),
    });

    expect(mine.html).not.toContain('icons-tab-delete-note');
    // Delete selected says why it cannot delete the server's icon.
    const none = await renderServer({
      library: libraryWith({ icons: [ICONS[1]] }),
    });

    expect(none.html).toContain('bulk-delete-icons');
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
    // The usage counts the built-in icons too.
    expect(textOf(html)).toContain(
      `The library holds ${builtinIcons().length + 2} of 2000 icons.`,
    );
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

    // Each built-in card has a checkbox, View and Download, and no Delete.
    const firewall = html.slice(
      html.lastIndexOf('<li', html.indexOf('builtin-icon-card-firewall')),
      html.indexOf('</li>', html.indexOf('builtin-icon-card-firewall')),
    );

    expect(textOf(firewall)).toBe(
      'Firewall Select Firewall Key: firewall Built-in View Download not selected',
    );
    expect(firewall).toContain('data-testid="builtin-icon-select-firewall"');
    expect(firewall).toContain('builder-icon--eye');
    expect(firewall).toContain('builder-icon--download');
    expect(firewall).not.toContain('builder-icon--trash');
    const at = builtinIcons().findIndex((icon) => icon.key === 'firewall');

    expect(firewall).toContain(
      `aria-labelledby="builtin-icon-name-${at} builtin-icon-state-${at}"`,
    );
    expect(textOf(html)).toContain(
      'Download saves an icon as a PNG file of 96 by 96 pixels.',
    );
    expect(textOf(html)).toContain(
      `0 of ${builtinIcons().length} selected Download selected Delete selected`,
    );

    // The server's icons show when Show chooses them.
    const server = await renderServer({
      library: libraryWith({ icons: ICONS }),
    });

    expect(server.html).toContain('data-testid="icons-list"');
    expect(server.html).not.toContain('builtin-icons-list');
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
      /^Name Firewall Key firewall Source Built-in File PNG, 96 × 96 pixels Download Close/,
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
