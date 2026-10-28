// The drafts page's tabs and cards, and the toolbar's Exp, rendered on the
// server. Sizes, focus and the tooltip are left to builder-drafts.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';
import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
import { BUSY_GROUPS, runBusy } from '@/builder/busy.js';

import { tag, textOf } from './fixtures.js';
import { renderWithStore } from './render.js';

function render(props = {}) {
  return renderToString(
    createSSRApp({ render: () => h(BuilderDrafts, props) }),
  );
}

// The panel of one tab.
function panelOf(html, id) {
  const start = html.indexOf(`id="panel-${id}"`);
  const next = html.indexOf('id="panel-', start + 1);

  expect(start, id).toBeGreaterThan(-1);

  return html.slice(start, next < 0 ? undefined : next);
}

// The card of one listed item, by the test id of its first button.
function cardOf(panel, id) {
  const card = panel
    .split('<li ')
    .find((part) => new RegExp(`data-testid="draft-[a-z]+-${id}"`).test(part));

  expect(card, id).toBeTruthy();

  return card;
}

// The test ids of a card's buttons, in order, without the item's id.
function buttons(card) {
  const actions = card.slice(card.indexOf('builder-card__actions'));

  return [...actions.matchAll(/<button\b[^>]*data-testid="([^"]+)"/g)].map(
    ([, id]) => id.replace(/^(draft|published)-([a-z]+)-.*$/, '$1-$2'),
  );
}

// Every Builder user's drafts are listed together: alice is signed in, and
// erin created d2.
const drafts = [
  {
    id: 'd2',
    createdBy: 'erin',
    title: 'Plain',
    updated: '2026-09-27T08:00:00Z',
    lastModifiedBy: 'erin',
  },
  {
    id: 'd1',
    createdBy: 'alice',
    title: 'Network lab',
    updated: '2026-09-27T09:00:00Z',
    lastModifiedBy: 'bob',
  },
];

const damaged = [
  { id: 'x1', createdBy: 'erin', damaged: true },
  { id: 'x2', damaged: true },
];

describe('the tabs', () => {
  test('each says how many it lists, in a count of its own class', async () => {
    const html = await render({ drafts, published: [{ id: 'p1' }] });
    const tabs = html.slice(
      html.indexOf('role="tablist"'),
      html.indexOf('role="tabpanel"'),
    );

    expect(tabs).toMatch(
      /data-testid="drafts-tab-drafts"[^>]*>\s*Drafts\s*<span class="builder-tab__count"[^>]*>\(2\)<\/span>\s*<\/button>/,
    );
    expect(tabs).toMatch(
      /data-testid="drafts-tab-published"[^>]*>\s*Published Diagrams\s*<span class="builder-tab__count"[^>]*>\(1\)<\/span>/,
    );
    // The count is not the cards' small text, which sat low beside the name.
    expect(tabs).not.toContain('builder-card__meta');
    expect(tabs.match(/builder-tab__count/g)).toHaveLength(2);
    // Drafts is the tab shown first.
    expect(tag(tabs, 'data-testid="drafts-tab-drafts"')).toContain(
      'aria-selected="true"',
    );
  });

  test('a damaged draft is counted on the Drafts tab, which lists it', async () => {
    const html = await render({ drafts, damaged });

    expect(html).toMatch(
      /data-testid="drafts-tab-drafts"[^>]*>[^<]*<span[^>]*>\(4\)</,
    );
  });

  test('Drafts lists the latest changed first, whoever created them, then the damaged ones', async () => {
    const panel = panelOf(await render({ drafts, damaged }), 'drafts');
    const order = [...panel.matchAll(/data-testid="draft-card-([^"]+)"/g)].map(
      ([, id]) => id,
    );

    expect(order).toEqual(['d1', 'd2', 'x1', 'x2']);
  });

  test('with no drafts, Drafts says how to start one', async () => {
    const panel = panelOf(await render(), 'drafts');

    expect(panel).toContain(
      'There are no drafts yet. Start from a blank diagram.',
    );
  });
});

describe('Blank diagram', () => {
  test('shows a ring in place of its icon while it makes the draft, and keeps its name', async () => {
    const blank = 'data-testid="drafts-blank"';
    const idle = tag(await render({ drafts }), blank);

    expect(idle).not.toContain('aria-busy');
    expect(idle).not.toContain('aria-disabled');

    // Another action keeps it waiting, without a ring.
    expect(tag(await render({ drafts, busy: true }), blank)).toContain(
      'aria-disabled="true"',
    );

    let done;
    const making = runBusy(
      BUSY_GROUPS.blank,
      'blank',
      () =>
        new Promise((resolve) => {
          done = resolve;
        }),
    );
    const html = await render({ drafts, busy: true });
    const button = tag(html, blank);

    expect(button).toContain('aria-busy="true"');
    expect(button).toContain('aria-disabled="true"');
    expect(html).toMatch(
      /data-testid="drafts-blank"[^>]*>(?:\s|<!--[^>]*-->)*<span class="builder-toolbar__spinner builder-busy-button__ring" aria-hidden="true"[\s\S]*?Blank diagram/,
    );

    await vi.waitFor(() => expect(done).toBeTypeOf('function'));
    done();
    await making;
    expect(tag(await render({ drafts }), blank)).not.toContain('aria-busy');
  });
});

describe('a draft card', () => {
  test('says who created it on one line, then when and by whom it changed', async () => {
    const card = cardOf(panelOf(await render({ drafts }), 'drafts'), 'd1');
    const lines = [
      ...card.matchAll(
        /<p\b[^>]*class="builder-card__meta"[^>]*>([\s\S]*?)<\/p>/g,
      ),
    ].map(([, text]) =>
      text
        .replace(/<[^>]+>/g, '')
        .replace(/\s+/g, ' ')
        .trim(),
    );

    expect(lines).toHaveLength(2);
    expect(lines[0]).toBe('Created by alice');
    expect(lines[1]).toMatch(/^Updated .+ by bob$/);
    expect(tag(card, 'data-testid="draft-created-by-d1"')).toMatch(/^<p /);
    expect(tag(card, 'data-testid="card-time-d1"')).toMatch(/^<p /);
    // No line starts with the separator a single line needed.
    expect(lines.filter((line) => line.startsWith('·'))).toEqual([]);
    // The name comes first, in the card's head row.
    expect(card.indexOf('builder-card__head')).toBeLessThan(
      card.indexOf('builder-card__meta'),
    );
    // The name starts the head row, and its checkbox follows it, at the
    // top right: nothing else is in the row.
    expect(card.replace(/<!--[\s\S]*?-->/g, '')).toMatch(
      /class="builder-card__head"[^>]*>\s*<h2[^>]*>\s*Network lab[\s\S]*?<\/h2>\s*<label class="builder-card__select"[\s\S]*?<\/label>\s*<\/div>/,
    );

    // The one who created it changed it last: they are named on both lines.
    const own = cardOf(panelOf(await render({ drafts }), 'drafts'), 'd2');

    expect(own).toContain('Created by erin');
    expect(own).toMatch(
      /data-testid="card-time-d2"[^>]*>\s*Updated [^<]+ by erin\s*</,
    );
  });

  test('has Open, Delete and Publish, in that order, in one grid, on every draft', async () => {
    const panel = panelOf(await render({ drafts }), 'drafts');

    for (const id of ['d1', 'd2']) {
      expect(buttons(cardOf(panel, id))).toEqual([
        'draft-open',
        'draft-duplicate',
        'draft-delete',
        'draft-publish',
      ]);
    }
    expect(panel.match(/class="builder-card__actions"/g)).toHaveLength(2);
    // The page header keeps its own row of buttons.
    expect(panel).not.toContain('builder-drafts__actions');
  });

  test('Delete names the draft and its time, and asks first', async () => {
    const panel = panelOf(await render({ drafts }), 'drafts');
    const remove = tag(panel, 'data-testid="draft-delete-d2"');

    expect(remove).toMatch(/aria-label="Delete Plain, updated [^"]+"/);
    expect(remove).toContain('aria-haspopup="dialog"');
    expect(remove).toContain('builder-button--danger');
    expect(remove).not.toContain('aria-disabled');
  });

  // The shared item actions: one icon each, Open in blue and Delete in red.
  test('each button has the icon and color of its action, and one structure', async () => {
    const html = await render({
      drafts,
      published: [
        { id: 'p1', kind: 'Topology', target: 'lab', source: 'store' },
      ],
    });
    const icons = {
      'draft-open-d1': 'folder-open',
      'draft-duplicate-d1': 'copy',
      'draft-delete-d1': 'trash',
      'draft-publish-d1': 'publish',
      'draft-open-p1': 'folder-open',
      'published-delete-p1': 'trash',
    };

    for (const [id, icon] of Object.entries(icons)) {
      const button = html.slice(html.indexOf(`data-testid="${id}"`));
      const opening = tag(html, `data-testid="${id}"`);

      expect(button.slice(0, button.indexOf('</button>')), id).toContain(
        `builder-icon--${icon}`,
      );
      expect(opening, id).toContain('builder-action-button');
      expect(opening.includes('builder-button--danger'), id).toBe(
        icon === 'trash',
      );
      expect(opening.includes('builder-button--primary'), id).toBe(
        icon === 'folder-open',
      );
    }
  });

  test('a busy button says what it does without an ellipsis', async () => {
    const html = await render({
      drafts,
      published: [
        { id: 'p1', kind: 'Topology', target: 'lab', source: 'store' },
      ],
      busy: true,
      opening: 'd1',
      duplicating: 'd2',
      deletingPublished: ['p1'],
    });
    const label = (id) => {
      const button = html.slice(html.indexOf(`data-testid="${id}"`));

      return textOf(
        button.slice(button.indexOf('>') + 1, button.indexOf('</button>')),
      );
    };

    expect(label('draft-open-d1')).toBe('Open Opening');
    expect(label('draft-duplicate-d2')).toBe('Duplicate Duplicating');
    expect(label('published-delete-p1')).toBe('Delete Deleting');
    expect(html).not.toMatch(/(Opening|Duplicating|Deleting|Loading)…/);
    expect(html).toMatch(
      /data-testid="draft-open-d1"[\s\S]*?<span class="is-off">(<!--\[-->)?Open(<!--\]-->)?<\/span><span class="">Opening<\/span>/,
    );
  });

  test('Publish names the draft, opens a dialog, and says while the draft loads', async () => {
    const idle = tag(
      await render({ drafts }),
      'data-testid="draft-publish-d1"',
    );

    expect(idle).toMatch(/^<button/);
    expect(idle).toMatch(/aria-label="Publish Network lab, updated [^"]+"/);
    expect(idle).toContain('aria-haspopup="dialog"');
    expect(idle).not.toContain('aria-disabled');
    expect(idle).not.toContain('aria-busy');

    // While it loads, every button that would open another waits.
    const html = await render({ drafts, busy: true, publishing: 'd1' });
    const loading = tag(html, 'data-testid="draft-publish-d1"');
    const other = tag(html, 'data-testid="draft-publish-d2"');

    expect(loading).toMatch(/aria-label="Loading Network lab, updated [^"]+"/);
    expect(loading).toContain('aria-disabled="true"');
    expect(loading).toContain('aria-busy="true"');
    expect(other).toMatch(/aria-label="Publish Plain, updated [^"]+"/);
    expect(other).toContain('aria-disabled="true"');
    expect(other).not.toContain('aria-busy');
    // Both labels hold the button's width. The one not shown is hidden.
    expect(html).toMatch(
      /data-testid="draft-publish-d1"[\s\S]*?<span class="is-off">(<!--\[-->)?Publish(<!--\]-->)?<\/span><span class="">Loading<\/span>/,
    );
  });

  // A draft being published from its card, whose dialog was closed, must
  // not be deleted meanwhile: Delete waits with the other buttons.
  test('while a draft is made, opened or published, no card can be deleted', async () => {
    const lists = {
      drafts,
      damaged,
      published: [
        { id: 'p1', kind: 'Topology', target: 'lab', source: 'store' },
      ],
    };
    const deletes = [
      'data-testid="draft-delete-d1"',
      'data-testid="draft-delete-d2"',
      'data-testid="draft-delete-x1"',
      'data-testid="published-delete-p1"',
    ];
    const idle = await render(lists);
    const busy = await render({ ...lists, busy: true });

    for (const button of deletes) {
      expect(tag(idle, button)).not.toContain('aria-disabled');
      expect(tag(busy, button)).toContain('aria-disabled="true"');
      // It waits. No topology is being deleted.
      expect(tag(busy, button)).not.toContain('aria-busy');
    }
  });

  test('Publish is on every readable draft while the role may publish, and no other', async () => {
    const lists = {
      drafts,
      damaged,
      published: [{ id: 'p1', kind: 'Topology', target: 'lab' }],
    };
    const publishes = (html) =>
      [...html.matchAll(/data-testid="draft-publish-([^"]+)"/g)]
        .map(([, id]) => id)
        .sort();

    expect(publishes(await render(lists))).toEqual(['d1', 'd2']);
    // Publishing writes phenix configs, which the role must allow.
    expect(publishes(await render({ ...lists, canPublish: false }))).toEqual(
      [],
    );
  });

  test('a damaged draft says why it cannot be opened, and has Delete only', async () => {
    const panel = panelOf(await render({ drafts, damaged }), 'drafts');
    const card = cardOf(panel, 'x1');

    expect(buttons(card)).toEqual(['draft-delete']);
    expect(card).toContain('Created by erin');
    expect(card).toMatch(
      /data-testid="draft-damaged-x1"[\s\S]*?You cannot open this draft, because the Builder cannot read it\./,
    );
    expect(tag(panel, 'data-testid="draft-delete-x2"')).toContain(
      'aria-label="Delete Damaged draft"',
    );
  });
});

describe('a published diagram’s card', () => {
  const published = [
    {
      id: 'p1',
      kind: 'Topology',
      source: 'store',
      target: 'lab',
      createdAt: '2026-09-27T09:00:00Z',
      experiment: 'lab-exp',
    },
    {
      id: 'p2',
      kind: 'Topology',
      source: 'store',
      target: 'plain',
      createdAt: '2026-09-27T10:00:00Z',
    },
    {
      id: 'file/plant',
      kind: 'Topology',
      source: 'file',
      target: 'plant',
      path: '/phenix/topologies/plant/plant.builder.json',
    },
  ];

  test('has Exp, between Open and Delete, only while its publication has an experiment', async () => {
    const panel = panelOf(await render({ published }), 'published');

    expect(buttons(cardOf(panel, 'p1'))).toEqual([
      'draft-open',
      'published-experiment',
      'published-delete',
    ]);
    expect(buttons(cardOf(panel, 'p2'))).toEqual([
      'draft-open',
      'published-delete',
    ]);
    expect(buttons(cardOf(panel, 'file/plant'))).toEqual(['draft-open']);
    expect(panel.match(/published-experiment-/g)).toHaveLength(1);
    // A published diagram is not published from here.
    expect(panel).not.toContain('draft-publish-');
  });

  test('Exp is named for the experiment it opens, and opens no dialog', async () => {
    const html = await render({ published });
    const exp = tag(html, 'data-testid="published-experiment-p1"');

    expect(exp).toMatch(/^<button/);
    // The name starts with the visible label (WCAG 2.5.3).
    expect(exp).toContain('aria-label="Exp: open experiment lab-exp"');
    expect(exp).not.toContain('aria-haspopup');
    expect(exp).not.toContain('aria-disabled');
    expect(html).toMatch(
      /data-testid="published-experiment-p1"[^>]*>(?:\s|<!--[^>]*-->)*<svg[^>]*builder-icon--experiment[\s\S]*?<\/svg>(?:\s|<!--[^>]*-->)*Exp(?:\s|<!--[^>]*-->)*<\/button>/,
    );
    // The tooltip shows on hover and focus only.
    expect(html).not.toContain('drafts-tooltip');

    expect(
      tag(
        await render({ published, busy: true }),
        'data-testid="published-experiment-p1"',
      ),
    ).toContain('aria-disabled="true"');
  });

  test('its time is on a line of its own, and a file’s path in its place', async () => {
    const panel = panelOf(await render({ published }), 'published');

    expect(panel).toMatch(
      /<p[^>]*data-testid="card-time-p1"[^>]*>\s*Published [^<]+<\/p>/,
    );
    expect(panel).not.toContain('card-time-file/plant');
    expect(tag(panel, 'data-testid="published-path-file/plant"')).toMatch(
      /^<p /,
    );
    // A published diagram names no creator.
    expect(panel).not.toContain('Created by');
  });

  test('a published topology’s Delete names it, and says while it deletes', async () => {
    const lists = {
      published: [
        ...published,
        { id: 'p3', kind: 'Experiment', source: 'store', target: 'run' },
      ],
    };
    const remove = tag(
      await render(lists),
      'data-testid="published-delete-p1"',
    );

    expect(remove).toMatch(/aria-label="Delete lab, published [^"]+"/);
    expect(remove).toContain('aria-haspopup="dialog"');
    expect(remove).not.toContain('aria-disabled');
    // A published experiment is deleted from the Experiments page.
    expect(await render(lists)).not.toContain('published-delete-p3');

    const deleting = tag(
      await render({ ...lists, deletingPublished: ['p1'] }),
      'data-testid="published-delete-p1"',
    );

    expect(deleting).toMatch(/aria-label="Deleting lab, published [^"]+"/);
    expect(deleting).toContain('aria-disabled="true"');
    expect(deleting).toContain('aria-busy="true"');
  });

  // A topology whose diagram is read from the Builder file it names is
  // listed by a handle, with the file's path and no published document.
  test('a topology read from its Builder file is tagged File, says where from, and has no Delete', async () => {
    const panel = panelOf(await render({ published }), 'published');
    const card = cardOf(panel, 'file/plant');

    // The tag is a word in the card's heading, not a color or an icon.
    expect(panel).toMatch(
      /<h2[^>]*>\s*plant\s*<span[^>]*data-testid="published-file-file\/plant"[^>]*>File<\/span>/,
    );
    expect(card).toMatch(
      />\s*Read from \/phenix\/topologies\/plant\/plant\.builder\.json\s*</,
    );
    expect(card).not.toContain('Published');
    // Open names the file, as a published diagram's names its time.
    expect(tag(panel, 'data-testid="draft-open-file/plant"')).toContain(
      'aria-label="Open plant, read from /phenix/topologies/plant/plant.builder.json"',
    );
    expect(panel).not.toContain('published-file-p1');
    expect(panel.match(/builder-drafts__tag/g)).toHaveLength(1);
  });
});

describe('the toolbar’s Exp', () => {
  const toolbar = async (prepare) =>
    (await renderWithStore(BuilderToolbar, { prepare })).html;

  test('is last of the ways out, only while the diagram was published with an experiment', async () => {
    expect(await toolbar()).not.toContain('toolbar-experiment');

    const some = await toolbar((store) => {
      store.experiment = 'lab-exp';
    });
    const exp = tag(some, 'data-testid="toolbar-experiment"');

    expect(exp).toMatch(/^<button/);
    // After Publish, before the next group's Minimap.
    expect(some.indexOf('toolbar-experiment')).toBeGreaterThan(
      some.indexOf('toolbar-publish'),
    );
    expect(some.indexOf('toolbar-experiment')).toBeLessThan(
      some.indexOf('toolbar-minimap'),
    );
    // It leaves the Builder, so it announces no dialog. No keys describe it.
    expect(exp).toContain('aria-label="Exp: open experiment lab-exp"');
    expect(exp).not.toContain('aria-haspopup');
    expect(exp).not.toContain('aria-disabled');
    expect(exp).not.toContain('aria-describedby');
    expect(some).toMatch(
      /data-testid="toolbar-experiment"[^>]*>(?:\s|<!--[^>]*-->)*<svg[^>]*builder-icon--experiment[\s\S]*?<\/svg>(?:\s|<!--[^>]*-->)*Exp(?:\s|<!--[^>]*-->)*<\/button>/,
    );
  });

  test('shows a ring in place of its icon while it opens the experiment, from any control', async () => {
    let done;
    const opening = runBusy(
      BUSY_GROUPS.experiment,
      'lab-exp',
      () =>
        new Promise((resolve) => {
          done = resolve;
        }),
    );
    const html = await toolbar((store) => {
      store.experiment = 'lab-exp';
    });
    const exp = tag(html, 'data-testid="toolbar-experiment"');

    expect(exp).toContain('aria-busy="true"');
    // Its name stays.
    expect(exp).toContain('aria-label="Exp: open experiment lab-exp"');
    expect(html).toMatch(
      /data-testid="toolbar-experiment"[^>]*>(?:\s|<!--[^>]*-->)*<span class="builder-toolbar__spinner builder-busy-button__ring" aria-hidden="true"/,
    );

    await vi.waitFor(() => expect(done).toBeTypeOf('function'));
    done();
    await opening;
    expect(
      tag(
        await toolbar((store) => {
          store.experiment = 'lab-exp';
        }),
        'data-testid="toolbar-experiment"',
      ),
    ).not.toContain('aria-busy');
  });

  test('shows for a published diagram opened read only, from its listed row', async () => {
    const viewed = (documents) =>
      toolbar((store) => {
        store.readOnly = true;
        store.published = { id: 'p1', name: 'lab', target: 'lab' };
        store.documents = documents;
      });

    expect(
      tag(
        await viewed([{ id: 'p1', target: 'lab', experiment: 'lab-exp' }]),
        'data-testid="toolbar-experiment"',
      ),
    ).toContain('aria-label="Exp: open experiment lab-exp"');
    expect(await viewed([{ id: 'p1', target: 'lab' }])).not.toContain(
      'toolbar-experiment',
    );
    // Another diagram's experiment is not this one's.
    expect(
      await viewed([{ id: 'p2', target: 'edge', experiment: 'edge-exp' }]),
    ).not.toContain('toolbar-experiment');
  });
});

describe('selecting cards', () => {
  const published = [
    { id: 'p1', kind: 'Topology', target: 'lab', source: 'store' },
    { id: 'p2', kind: 'Experiment', target: 'exp', source: 'store' },
    {
      id: 'file/site',
      kind: 'Topology',
      target: 'site',
      source: 'file',
      path: '/phenix/site.builder.json',
    },
  ];
  const selects = (panel) =>
    [...panel.matchAll(/data-testid="card-select-([^"]+)"/g)].map(
      ([, id]) => id,
    );

  test('a draft card has a checkbox, named for the card, after its name', async () => {
    const panel = panelOf(await render({ drafts }), 'drafts');
    const card = cardOf(panel, 'd1');
    const box = tag(card, 'data-testid="card-select-d1"');

    expect(selects(panel)).toEqual(['d1', 'd2']);
    expect(box).toMatch(/^<input/);
    expect(box).toContain('type="checkbox"');
    expect(box).not.toContain('checked');
    expect(box).not.toContain('disabled');
    // Its label names the card as the card's buttons do.
    expect(card).toMatch(
      /<label class="builder-card__select"[^>]*><input[^>]*><span class="builder-visually-hidden"[^>]*>\s*Select Network lab, updated [^<]+<\/span><\/label>/,
    );
    // At the top right: after the name, in the DOM as on the screen.
    expect(card.indexOf('builder-card__select')).toBeGreaterThan(
      card.indexOf('</h2>'),
    );
    // Nothing is selected yet, so no card is framed.
    expect(card).toMatch(/^class="builder-card builder-panel"/);
  });

  test('every draft can be selected, damaged ones too, whoever created it', async () => {
    const panel = panelOf(await render({ drafts, damaged }), 'drafts');

    expect(selects(panel)).toEqual(['d1', 'd2', 'x1', 'x2']);
    expect(panel).toMatch(
      /data-testid="bulk-count-drafts"[^>]*>\s*0 of 4 selected\s*</,
    );
    expect(panel).toContain('data-testid="bulk-download-drafts"');
    expect(panel).toContain('data-testid="bulk-delete-drafts"');
  });

  test('Download selected is described by the count, and waits for a selection', async () => {
    const panel = panelOf(await render({ drafts }), 'drafts');
    const download = tag(panel, 'data-testid="bulk-download-drafts"');

    expect(download).toMatch(/^<button/);
    expect(download).toContain('aria-describedby="bulk-count-drafts"');
    expect(download).toContain('builder-action-button--download');
    expect(download).toContain('aria-disabled="true"');
    // It saves files: it opens no dialog.
    expect(download).not.toContain('aria-haspopup');
    expect(panel).toMatch(
      /data-testid="bulk-download-drafts"[^>]*>[\s\S]*?Download selected[\s\S]*?<\/button>/,
    );
    // The row has no note on several downloads.
    expect(panel).not.toContain('bulk-download-note');
    expect(panel).not.toContain('several downloads');
    // Download selected, then Delete selected.
    expect(panel.indexOf('bulk-download-drafts')).toBeLessThan(
      panel.indexOf('bulk-delete-drafts'),
    );
  });

  test('the cards of a list are one Tab stop, each named by its title and whether it is selected', async () => {
    const panel = panelOf(await render({ drafts }), 'drafts');
    const first = tag(panel, 'data-testid="draft-card-d1"');
    const second = tag(panel, 'data-testid="draft-card-d2"');

    expect(first).toMatch(/^<li/);
    expect(first).toContain('tabindex="0"');
    expect(second).toContain('tabindex="-1"');
    expect(first).toContain(
      'aria-labelledby="card-name-drafts-0 card-state-drafts-0"',
    );
    expect(panel).toMatch(/<h2 id="card-name-drafts-0"[^>]*>\s*Network lab/);
    expect(panel).toMatch(
      /<span id="card-state-drafts-0" hidden[^>]*>not selected<\/span>/,
    );

    // A card that cannot be selected (a topology read from its Builder
    // file) is named by its title alone.
    const files = panelOf(
      await render({ published: [published[2]] }),
      'published',
    );

    expect(tag(files, 'data-testid="draft-card-file/site"')).toContain(
      'aria-labelledby="card-name-published-0"',
    );
    expect(files).not.toContain('card-state-published-0');
  });

  test('published diagrams are selected to download them, and topologies to delete them; a Builder file is not', async () => {
    const panel = panelOf(await render({ published }), 'published');

    expect(selects(panel)).toEqual(['p1', 'p2']);
    expect(panel).toMatch(
      /data-testid="bulk-count-published"[^>]*>\s*0 of 2 selected\s*</,
    );
    expect(panel).toContain('data-testid="bulk-download-published"');
    expect(panel).toContain('data-testid="bulk-delete-published"');

    // A role that may not delete configs still downloads them.
    const html = panelOf(
      await render({ published, canDeletePublished: false }),
      'published',
    );

    expect(selects(html)).toEqual(['p1', 'p2']);
    expect(html).toContain('data-testid="bulk-download-published"');
    expect(html).not.toContain('bulk-delete-published');
  });

  test('the row above the cards has Select all, the count, and the actions, which say why they cannot act', async () => {
    const panel = panelOf(await render({ drafts }), 'drafts');
    const bar = tag(panel, 'data-testid="bulk-bar-drafts"');
    const all = tag(panel, 'data-testid="bulk-all-drafts"');
    const remove = tag(panel, 'data-testid="bulk-delete-drafts"');

    expect(bar).toContain('role="group"');
    expect(bar).toContain('aria-label="Bulk actions: Drafts"');
    expect(all).toMatch(/^<input/);
    expect(all).toContain('type="checkbox"');
    expect(all).not.toContain('disabled');
    expect(panel).toMatch(
      /<label[^>]*><input[^>]*data-testid="bulk-all-drafts"[^>]*>\s*Select all\s*<\/label>/,
    );
    expect(panel).toMatch(
      /<span id="bulk-count-drafts"[^>]*data-testid="bulk-count-drafts"[^>]*>\s*0 of 2 selected\s*<\/span>/,
    );
    // The row comes before the cards.
    expect(panel.indexOf('bulk-bar-drafts')).toBeLessThan(
      panel.indexOf('drafts-list-drafts'),
    );

    expect(remove).toMatch(/^<button/);
    expect(remove).toContain('aria-haspopup="dialog"');
    expect(remove).toContain('aria-describedby="bulk-count-drafts"');
    // Nothing is selected yet.
    expect(remove).toContain('aria-disabled="true"');
    expect(remove).toContain('builder-button--danger');
    expect(panel).toMatch(
      /data-testid="bulk-delete-drafts"[^>]*>[\s\S]*?builder-icon--trash[\s\S]*?Delete selected[\s\S]*?<\/button>/,
    );
    expect(panel).not.toContain('bulk-progress-');
    expect(panel).not.toContain('aria-busy');
  });

  test('while a batch runs, its row says how far it is, and nothing can be selected or deleted', async () => {
    const html = await render({
      drafts,
      published,
      busy: true,
      bulk: { tab: 'drafts', label: 'Deleting', done: 2, total: 5 },
    });
    const panel = panelOf(html, 'drafts');

    expect(panel).toMatch(
      /data-testid="bulk-progress-drafts"[^>]*>[\s\S]*?Deleting 2 of 5…\s*<\/span>/,
    );
    // The progress is not a live region: the page says what it came to.
    expect(tag(panel, 'data-testid="bulk-progress-drafts"')).not.toMatch(
      /role=|aria-live/,
    );
    expect(tag(panel, 'data-testid="bulk-all-drafts"')).toContain('disabled');
    expect(tag(panel, 'data-testid="card-select-d1"')).toContain('disabled');
    expect(tag(panel, 'data-testid="drafts-list-drafts"')).toContain(
      'aria-busy="true"',
    );
    expect(tag(panel, 'data-testid="draft-delete-d1"')).toContain(
      'aria-disabled="true"',
    );

    // Another tab's row shows no progress, and its checkboxes wait too.
    const other = panelOf(html, 'published');

    expect(other).not.toContain('bulk-progress-');
    expect(tag(other, 'data-testid="drafts-list-published"')).not.toContain(
      'aria-busy',
    );
    expect(tag(other, 'data-testid="bulk-all-published"')).toContain(
      'disabled',
    );
    expect(tag(other, 'data-testid="card-select-p1"')).toContain('disabled');
    expect(tag(other, 'data-testid="published-delete-p1"')).toContain(
      'aria-disabled="true"',
    );
  });

  test('what a batch left undone is listed under the row of its tab, as an alert', async () => {
    const bulkResult = {
      tab: 'drafts',
      heading: '1 of 3 drafts could not be deleted. The other 2 were deleted.',
      items: [
        {
          key: 'd1',
          name: 'Network lab, updated Sep 27, 2026',
          reason: 'It changed on the server during the delete. Try again.',
        },
      ],
    };
    const html = await render({ drafts, published, bulkResult });
    const panel = panelOf(html, 'drafts');
    const summary = tag(panel, 'data-testid="bulk-summary"');
    const [, titleId] = summary.match(/aria-labelledby="([^"]+)"/);

    expect(summary).toContain('role="alert"');
    expect(summary).toContain('tabindex="-1"');
    expect(panel).toMatch(
      new RegExp(
        `<h3 id="${titleId}"[^>]*>1 of 3 drafts could not be deleted\\. The other 2 were deleted\\.</h3>`,
      ),
    );
    expect(panel).toMatch(
      /<li[^>]*>\s*Network lab, updated Sep 27, 2026: It changed on the server during the delete\. Try again\.\s*<\/li>/,
    );
    expect(panel).toMatch(
      /data-testid="bulk-summary-dismiss"[^>]*>\s*Dismiss\s*<\/button>/,
    );
    // Under the row, above the cards.
    expect(panel.indexOf('bulk-bar-drafts')).toBeLessThan(
      panel.indexOf('data-testid="bulk-summary"'),
    );
    expect(panel.indexOf('data-testid="bulk-summary"')).toBeLessThan(
      panel.indexOf('drafts-list-drafts'),
    );
    expect(html.match(/data-testid="bulk-summary"/g)).toHaveLength(1);
    expect(panelOf(html, 'published')).not.toContain('bulk-summary');

    // The tab the batch was for is gone: the summary is still shown, on
    // Drafts.
    const moved = await render({
      drafts,
      bulkResult: { ...bulkResult, tab: 'gone' },
    });

    expect(panelOf(moved, 'drafts')).toContain('data-testid="bulk-summary"');
  });
});

describe('the Node Templates, Layers and Icons tabs', () => {
  const tabIds = (html) =>
    [...html.matchAll(/data-testid="drafts-tab-([a-z]+)"/g)].map(
      ([, id]) => id,
    );

  function renderWith(props) {
    return renderToString(
      createSSRApp({
        render: () =>
          h(BuilderDrafts, props, {
            templates: () => h('p', { id: 'the-templates' }, 'Templates'),
            icons: () => h('p', { id: 'the-icons' }, 'Icons'),
            layers: () => h('p', { id: 'the-layers' }, 'Layers'),
          }),
      }),
    );
  }

  test('is not there until the view has one', async () => {
    const html = await renderWith({ drafts });

    expect(tabIds(html)).toEqual(['drafts', 'published']);
    expect(html).not.toContain('the-templates');
    expect(html).not.toContain('panel-templates');
  });

  test('Node Templates stands after Published Diagrams, with its count and the view’s panel', async () => {
    const html = await renderWith({ drafts, templates: { count: 7 } });

    expect(tabIds(html)).toEqual(['drafts', 'published', 'templates']);
    expect(html).toMatch(
      /data-testid="drafts-tab-templates"[^>]*>\s*Node Templates\s*<span class="builder-tab__count"[^>]*>\(7\)<\/span>/,
    );

    const panel = panelOf(html, 'templates');

    expect(tag(html, 'id="panel-templates"')).toContain(
      'aria-labelledby="tab-templates"',
    );
    // The panel is the view's: no list, no row, and no text of its own.
    expect(panel).toContain('<p id="the-templates">Templates</p>');
    expect(panel).not.toContain('builder-cards');
    expect(panel).not.toContain('bulk-bar-');
    expect(panel).not.toContain('Loading…');
    // Its content takes focus, not the panel.
    expect(tag(html, 'id="panel-templates"')).not.toContain('tabindex');
  });

  test('Icons stands directly after Node Templates, with its count and the view’s panel', async () => {
    const html = await renderWith({
      drafts,
      templates: { count: 7 },
      icons: { count: 3 },
    });

    expect(tabIds(html)).toEqual(['drafts', 'published', 'templates', 'icons']);
    expect(html).toMatch(
      /data-testid="drafts-tab-icons"[^>]*>\s*Icons\s*<span class="builder-tab__count"[^>]*>\(3\)<\/span>/,
    );
    expect(tag(html, 'id="panel-icons"')).toContain(
      'aria-labelledby="tab-icons"',
    );
    expect(tag(html, 'id="panel-icons"')).toContain('hidden');

    // While the view has not read the icon library, the count is not
    // known: the tab shows no number rather than a false 0.
    const unknown = await renderWith({
      drafts,
      templates: { count: 7 },
      icons: { count: null },
    });

    expect(unknown).toMatch(
      /data-testid="drafts-tab-icons"[^>]*>\s*Icons\s*(<!--[^>]*-->\s*)?<\/button>/,
    );
    expect(panelOf(html, 'icons')).toContain('<p id="the-icons">Icons</p>');
    expect(panelOf(html, 'icons')).not.toContain('the-templates');
    expect(panelOf(html, 'templates')).not.toContain('the-icons');
    expect(tag(html, 'id="panel-icons"')).not.toContain('tabindex');
  });

  test('Layers stands between Node Templates and Icons, with its count and the view’s panel', async () => {
    const html = await renderWith({
      drafts,
      templates: { count: 7 },
      layers: { count: 2 },
      icons: { count: 3 },
    });

    expect(tabIds(html)).toEqual([
      'drafts',
      'published',
      'templates',
      'layers',
      'icons',
    ]);
    expect(html).toMatch(
      /data-testid="drafts-tab-layers"[^>]*>\s*Layers\s*<span class="builder-tab__count"[^>]*>\(2\)<\/span>/,
    );
    expect(tag(html, 'id="panel-layers"')).toContain(
      'aria-labelledby="tab-layers"',
    );
    expect(panelOf(html, 'layers')).toContain('<p id="the-layers">Layers</p>');
    expect(tag(html, 'id="panel-layers"')).not.toContain('tabindex');
  });
});
