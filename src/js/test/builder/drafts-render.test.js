// The drafts page's tabs and cards, rendered on the server: the tab counts,
// the lines of a card, its buttons and their order, and which cards have
// Publish, Delete and Exp. Sizes, focus and the tooltip need a browser, so
// they are left to builder-drafts.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

// The component reads no store; what it imports does.
vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';

function render(props = {}) {
  return renderToString(
    createSSRApp({ render: () => h(BuilderDrafts, props) }),
  );
}

// The attributes of the one element the pattern finds.
function tag(html, pattern) {
  const found = html.match(new RegExp(`<[a-z]+\\b[^>]*${pattern}[^>]*>`));

  expect(found, pattern).not.toBeNull();

  return found[0];
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

const mine = [
  {
    id: 'd1',
    owner: 'alice',
    title: 'Network lab',
    access: 'owner',
    canShare: true,
    canDelete: true,
    readOnly: false,
    updated: '2026-09-27T09:00:00Z',
    lastModifiedBy: 'bob',
    shares: [{ user: 'bob', access: 'edit' }],
  },
  {
    id: 'd2',
    owner: 'alice',
    title: 'Plain',
    access: 'owner',
    readOnly: false,
    updated: '2026-09-27T08:00:00Z',
    lastModifiedBy: 'alice',
  },
];

describe('the tabs', () => {
  test('each says how many it lists, in a count of its own class', async () => {
    const html = await render({ mine, shared: [], published: [{ id: 'p1' }] });
    const tabs = html.slice(
      html.indexOf('role="tablist"'),
      html.indexOf('role="tabpanel"'),
    );

    expect(tabs).toMatch(
      /data-testid="drafts-tab-mine"[^>]*>\s*My Drafts\s*<span class="builder-tab__count"[^>]*>\(2\)<\/span>\s*<\/button>/,
    );
    expect(tabs).toMatch(
      /data-testid="drafts-tab-shared"[^>]*>\s*Shared Drafts\s*<span class="builder-tab__count"[^>]*>\(0\)<\/span>/,
    );
    expect(tabs).toMatch(
      /data-testid="drafts-tab-published"[^>]*>\s*Published Diagrams\s*<span class="builder-tab__count"[^>]*>\(1\)<\/span>/,
    );
    // The count is not the cards' small text, which sat low beside the name.
    expect(tabs).not.toContain('builder-card__meta');
    expect(tabs.match(/builder-tab__count/g)).toHaveLength(3);
  });

  test('a damaged draft is counted on the tab that lists it', async () => {
    const html = await render({
      mine,
      damaged: {
        mine: [{ id: 'x1', owner: 'alice', damaged: true, canDelete: true }],
        others: [{ id: 'x2', owner: 'erin', damaged: true, canDelete: false }],
      },
    });

    expect(html).toMatch(
      /data-testid="drafts-tab-mine"[^>]*>[^<]*<span[^>]*>\(3\)</,
    );
    expect(html).toMatch(
      /data-testid="drafts-tab-others"[^>]*>[^<]*<span[^>]*>\(1\)</,
    );
  });
});

describe('a draft card', () => {
  test('says the owner on one line, the time on the next, then who it is shared with', async () => {
    const card = cardOf(panelOf(await render({ mine }), 'mine'), 'd1');
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

    expect(lines).toHaveLength(3);
    expect(lines[0]).toBe('Owner: alice');
    // Who changed it last is named when it was not the owner.
    expect(lines[1]).toMatch(/^Updated .+ by bob$/);
    expect(lines[2]).toBe('Shared with bob');
    expect(tag(card, 'data-testid="card-time-d1"')).toMatch(/^<p /);
    // No line starts with the separator a single line needed.
    expect(lines.filter((line) => line.startsWith('·'))).toEqual([]);
    // The name comes first, in the card's head row.
    expect(card.indexOf('builder-card__head')).toBeLessThan(
      card.indexOf('builder-card__meta'),
    );
    // Its checkbox stands before the name, and nothing else does.
    expect(
      card
        .replace(/<!--[\s\S]*?-->/g, '')
        .replace(/<label\b[\s\S]*?<\/label>/, ''),
    ).toMatch(/class="builder-card__head"[^>]*>\s*<h2[^>]*>\s*Network lab/);
  });

  test('has Open, Share, Delete and Publish, in that order, in one grid', async () => {
    const panel = panelOf(await render({ mine }), 'mine');

    expect(buttons(cardOf(panel, 'd1'))).toEqual([
      'draft-open',
      'draft-share',
      'draft-delete',
      'draft-publish',
    ]);
    // A button the user may not use is left out, and the rest close up.
    expect(buttons(cardOf(panel, 'd2'))).toEqual([
      'draft-open',
      'draft-delete',
      'draft-publish',
    ]);
    expect(panel.match(/class="builder-card__actions"/g)).toHaveLength(2);
    // The page header keeps its own row of buttons.
    expect(panel).not.toContain('builder-drafts__actions');
  });

  test('Publish names the draft, opens a dialog, and says while the draft loads', async () => {
    const idle = tag(await render({ mine }), 'data-testid="draft-publish-d1"');

    expect(idle).toMatch(/^<button/);
    expect(idle).toMatch(/aria-label="Publish Network lab, updated [^"]+"/);
    expect(idle).toContain('aria-haspopup="dialog"');
    expect(idle).not.toContain('aria-disabled');
    expect(idle).not.toContain('aria-busy');

    // While it loads, every button that would open another waits.
    const html = await render({ mine, busy: true, publishing: 'alice/d1' });
    const loading = tag(html, 'data-testid="draft-publish-d1"');
    const other = tag(html, 'data-testid="draft-publish-d2"');

    expect(loading).toMatch(/aria-label="Loading Network lab, updated [^"]+"/);
    expect(loading).toContain('aria-disabled="true"');
    expect(loading).toContain('aria-busy="true"');
    expect(other).toMatch(/aria-label="Publish Plain, updated [^"]+"/);
    expect(other).toContain('aria-disabled="true"');
    expect(other).not.toContain('aria-busy');
    // Both labels hold the button's width; the one not shown is hidden.
    expect(html).toMatch(
      /data-testid="draft-publish-d1"[\s\S]*?<span class="is-off"[^>]*>Publish<\/span><span class=""[^>]*>Loading…<\/span>/,
    );
  });

  // A draft being published from its card, whose dialog was closed, must
  // not be deleted meanwhile: Delete waits with the other buttons.
  test('while a draft is made, opened or published, no card can be deleted', async () => {
    const lists = {
      mine,
      published: [
        { id: 'p1', kind: 'Topology', target: 'lab', source: 'store' },
      ],
      others: [
        {
          id: 'r1',
          owner: 'erin',
          title: 'Theirs',
          access: 'edit',
          via: 'role',
          canDelete: true,
        },
      ],
    };
    const deletes = [
      'data-testid="draft-delete-d1"',
      'data-testid="draft-delete-r1"',
      'data-testid="published-delete-p1"',
    ];
    const idle = await render(lists);
    const busy = await render({ ...lists, busy: true });

    for (const button of deletes) {
      expect(tag(idle, button)).not.toContain('aria-disabled');
      expect(tag(busy, button)).toContain('aria-disabled="true"');
      // It waits; no topology is being deleted.
      expect(tag(busy, button)).not.toContain('aria-busy');
    }
  });

  test('Publish is on a draft the user may change, and no other', async () => {
    const html = await render({
      mine: [
        ...mine,
        // A role that may not change configs.
        { id: 'd3', owner: 'alice', title: 'Locked', readOnly: true },
      ],
      shared: [
        {
          id: 's1',
          owner: 'bob',
          access: 'view',
          via: 'share',
          readOnly: true,
        },
        {
          id: 's2',
          owner: 'bob',
          access: 'edit',
          via: 'share',
          readOnly: false,
        },
        {
          id: 's3',
          owner: 'bob',
          access: 'edit',
          via: 'share',
          readOnly: true,
        },
      ],
      others: [
        {
          id: 'r1',
          owner: 'erin',
          access: 'edit',
          via: 'role',
          readOnly: false,
        },
        {
          id: 'r2',
          owner: 'erin',
          access: 'view',
          via: 'role',
          readOnly: true,
        },
      ],
      damaged: {
        mine: [{ id: 'x1', owner: 'alice', damaged: true, canDelete: true }],
        others: [],
      },
      published: [{ id: 'p1', kind: 'Topology', target: 'lab' }],
    });
    const published = [...html.matchAll(/data-testid="draft-publish-([^"]+)"/g)]
      .map(([, id]) => id)
      .sort();

    expect(published).toEqual(['d1', 'd2', 'r1', 's2']);
  });
});

describe("Delete on other users' drafts", () => {
  const others = [
    {
      id: 'r1',
      owner: 'erin',
      title: 'Theirs',
      access: 'edit',
      via: 'role',
      canDelete: true,
      updated: '2026-09-27T09:00:00Z',
    },
    { id: 'r2', owner: 'erin', title: 'Kept', access: 'view', via: 'role' },
  ];

  test('is on the ones the server says the user may delete', async () => {
    const panel = panelOf(await render({ others }), 'others');
    const remove = tag(panel, 'data-testid="draft-delete-r1"');

    expect(remove).toMatch(/aria-label="Delete Theirs by erin, updated [^"]+"/);
    expect(remove).toContain('aria-haspopup="dialog"');
    expect(remove).toContain('builder-button--danger');
    expect(buttons(cardOf(panel, 'r1'))).toEqual([
      'draft-open',
      'draft-delete',
      'draft-publish',
    ]);
    expect(panel).not.toContain('draft-delete-r2');
    // Another user's draft is never shared from here.
    expect(panel).not.toContain('draft-share-');
  });

  test('does not follow what the role may do with the user’s own', async () => {
    // The role may not delete configs: none of the user's own has Delete,
    // and the row of another user's still decides for itself.
    const html = await render({ mine, others, canDelete: false });

    expect(panelOf(html, 'mine')).not.toContain('draft-delete-');
    expect(panelOf(html, 'others')).toContain('draft-delete-r1');
  });

  test('a draft shared with the user has none, whatever its row says', async () => {
    const panel = panelOf(
      await render({
        shared: [
          {
            id: 's1',
            owner: 'bob',
            title: 'Shared',
            access: 'edit',
            via: 'share',
            canDelete: true,
          },
        ],
      }),
      'shared',
    );

    expect(panel).toContain('draft-open-s1');
    expect(panel).not.toContain('draft-delete-s1');
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
      /data-testid="published-experiment-p1"[^>]*>\s*<svg[^>]*builder-icon--experiment[\s\S]*?<\/svg>\s*Exp\s*<\/button>/,
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
    // A published diagram names no owner.
    expect(panel).not.toContain('Owner:');
  });
});

describe('selecting cards', () => {
  const others = [
    {
      id: 'r1',
      owner: 'erin',
      title: 'Theirs',
      access: 'edit',
      via: 'role',
      canDelete: true,
      updated: '2026-09-27T09:00:00Z',
    },
    { id: 'r2', owner: 'erin', title: 'Kept', access: 'view', via: 'role' },
  ];
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

  test('a card the user may delete or share has a checkbox, named for the card, before its name', async () => {
    const panel = panelOf(await render({ mine }), 'mine');
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
    expect(card.indexOf('builder-card__select')).toBeLessThan(
      card.indexOf('<h2'),
    );
    // Nothing is selected yet, so no card is framed.
    expect(card).toMatch(/^class="builder-card builder-panel"/);
  });

  test('on My Drafts, a draft that can be neither deleted nor shared has none', async () => {
    // The role may not delete configs: only what can be shared is selected.
    const html = await render({
      mine,
      canDelete: false,
      damaged: {
        mine: [
          { id: 'x1', owner: 'alice', damaged: true, canDelete: true },
          { id: 'x2', owner: 'alice', damaged: true, canDelete: false },
        ],
        others: [],
      },
    });
    const panel = panelOf(html, 'mine');

    // d1 can be shared; a damaged draft follows what the server says of it.
    expect(selects(panel)).toEqual(['d1', 'x1']);
    expect(tag(panel, 'data-testid="bulk-count-mine"')).toBeTruthy();
    expect(panel).toMatch(
      /data-testid="bulk-count-mine"[^>]*>\s*0 of 2 selected\s*</,
    );
  });

  test('drafts shared with the user are never selected, and have no row', async () => {
    const html = await render({
      mine,
      shared: [
        {
          id: 's1',
          owner: 'bob',
          title: 'Shared',
          access: 'edit',
          via: 'share',
          canDelete: true,
        },
      ],
    });
    const panel = panelOf(html, 'shared');

    expect(panel).toContain('draft-open-s1');
    expect(selects(panel)).toEqual([]);
    expect(panel).not.toContain('bulk-bar-');
    expect(panel).not.toContain('builder-bulk');
  });

  test('other users’ drafts are selected where the user may delete them', async () => {
    const panel = panelOf(await render({ mine, others }), 'others');

    expect(selects(panel)).toEqual(['r1']);
    expect(panel).toMatch(
      /<span class="builder-visually-hidden"[^>]*>\s*Select Theirs by erin, updated [^<]+<\/span>/,
    );
    expect(panel).toContain('data-testid="bulk-delete-others"');
    // Another user's drafts are never shared from here.
    expect(panel).not.toContain('bulk-share-');

    // None the user may delete: no row, no checkboxes.
    const none = panelOf(await render({ others: [others[1]] }), 'others');

    expect(selects(none)).toEqual([]);
    expect(none).not.toContain('bulk-bar-');
  });

  test('published topologies are selected; experiments and Builder files are not', async () => {
    const panel = panelOf(await render({ published }), 'published');

    expect(selects(panel)).toEqual(['p1']);
    expect(panel).toMatch(
      /data-testid="bulk-count-published"[^>]*>\s*0 of 1 selected\s*</,
    );
    expect(panel).toContain('data-testid="bulk-delete-published"');

    // A role that may not delete configs selects nothing.
    const html = await render({ published, canDelete: false });

    expect(html).not.toContain('card-select-');
    expect(html).not.toContain('bulk-bar-');
  });

  test('the row above the cards has Select all, the count, and the actions, which say why they cannot act', async () => {
    const panel = panelOf(await render({ mine }), 'mine');
    const bar = tag(panel, 'data-testid="bulk-bar-mine"');
    const all = tag(panel, 'data-testid="bulk-all-mine"');
    const share = tag(panel, 'data-testid="bulk-share-mine"');
    const remove = tag(panel, 'data-testid="bulk-delete-mine"');

    expect(bar).toContain('role="group"');
    expect(bar).toContain('aria-label="Bulk actions: My Drafts"');
    expect(all).toMatch(/^<input/);
    expect(all).toContain('type="checkbox"');
    expect(all).not.toContain('disabled');
    expect(panel).toMatch(
      /<label[^>]*><input[^>]*data-testid="bulk-all-mine"[^>]*>\s*Select all\s*<\/label>/,
    );
    expect(panel).toMatch(
      /<span id="bulk-count-mine"[^>]*data-testid="bulk-count-mine"[^>]*>\s*0 of 2 selected\s*<\/span>/,
    );
    // The row comes before the cards.
    expect(panel.indexOf('bulk-bar-mine')).toBeLessThan(
      panel.indexOf('drafts-list-mine'),
    );

    for (const button of [share, remove]) {
      expect(button).toMatch(/^<button/);
      expect(button).toContain('aria-haspopup="dialog"');
      expect(button).toContain('aria-describedby="bulk-count-mine"');
      // Nothing is selected yet.
      expect(button).toContain('aria-disabled="true"');
    }
    expect(remove).toContain('builder-button--danger');
    expect(panel).toMatch(
      /data-testid="bulk-share-mine"[^>]*>[\s\S]*?Share selected\s*<\/button>/,
    );
    expect(panel).toMatch(
      /data-testid="bulk-delete-mine"[^>]*>\s*Delete selected\s*<\/button>/,
    );
    expect(panel).not.toContain('bulk-progress-');
    expect(panel).not.toContain('aria-busy');
  });

  test('Share selected is there only when a draft can be shared, and Delete selected when one can be deleted', async () => {
    const plain = panelOf(await render({ mine: [mine[1]] }), 'mine');

    expect(plain).not.toContain('bulk-share-mine');
    expect(plain).toContain('bulk-delete-mine');

    const shareOnly = panelOf(
      await render({ mine: [mine[0]], canDelete: false }),
      'mine',
    );

    expect(shareOnly).toContain('bulk-share-mine');
    expect(shareOnly).not.toContain('bulk-delete-mine');
  });

  test('while a batch runs, its row says how far it is, and nothing can be selected or deleted', async () => {
    const html = await render({
      mine,
      published,
      busy: true,
      bulk: { tab: 'mine', label: 'Deleting', done: 2, total: 5 },
    });
    const panel = panelOf(html, 'mine');

    expect(panel).toMatch(
      /data-testid="bulk-progress-mine"[^>]*>[\s\S]*?Deleting 2 of 5…\s*<\/span>/,
    );
    // The progress is not a live region: the page says what it came to.
    expect(tag(panel, 'data-testid="bulk-progress-mine"')).not.toMatch(
      /role=|aria-live/,
    );
    expect(tag(panel, 'data-testid="bulk-all-mine"')).toContain('disabled');
    expect(tag(panel, 'data-testid="card-select-d1"')).toContain('disabled');
    expect(tag(panel, 'data-testid="drafts-list-mine"')).toContain(
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
      tab: 'mine',
      heading: '1 of 3 drafts could not be deleted. The other 2 were deleted.',
      items: [
        {
          key: 'alice/d1',
          name: 'Network lab, updated Sep 27, 2026',
          reason:
            'It changed on the server while it was being deleted. Try again.',
        },
      ],
    };
    const html = await render({ mine, published, bulkResult });
    const panel = panelOf(html, 'mine');
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
      /<li[^>]*>\s*Network lab, updated Sep 27, 2026: It changed on the server while it was being deleted\. Try again\.\s*<\/li>/,
    );
    expect(panel).toMatch(
      /data-testid="bulk-summary-dismiss"[^>]*>\s*Dismiss\s*<\/button>/,
    );
    // Under the row, above the cards.
    expect(panel.indexOf('bulk-bar-mine')).toBeLessThan(
      panel.indexOf('data-testid="bulk-summary"'),
    );
    expect(panel.indexOf('data-testid="bulk-summary"')).toBeLessThan(
      panel.indexOf('drafts-list-mine'),
    );
    expect(html.match(/data-testid="bulk-summary"/g)).toHaveLength(1);
    expect(panelOf(html, 'published')).not.toContain('bulk-summary');

    // The tab the batch was for is gone (the last of other users' drafts):
    // the summary is still shown, on My Drafts.
    const moved = await render({
      mine,
      bulkResult: { ...bulkResult, tab: 'others' },
    });

    expect(panelOf(moved, 'mine')).toContain('data-testid="bulk-summary"');
  });
});

describe('the Node Templates tab', () => {
  const tabIds = (html) =>
    [...html.matchAll(/data-testid="drafts-tab-([a-z]+)"/g)].map(
      ([, id]) => id,
    );
  const others = [{ id: 'r1', owner: 'erin', access: 'view', via: 'role' }];

  function renderWith(props) {
    return renderToString(
      createSSRApp({
        render: () =>
          h(BuilderDrafts, props, {
            templates: () => h('p', { id: 'the-templates' }, 'Templates'),
          }),
      }),
    );
  }

  test('is not there until the view has one', async () => {
    const html = await renderWith({ mine, others });

    expect(tabIds(html)).toEqual(['mine', 'shared', 'published', 'others']);
    expect(html).not.toContain('the-templates');
    expect(html).not.toContain('panel-templates');
  });

  test('stands after Published Diagrams and before other users’ drafts, with its count and the view’s panel', async () => {
    const html = await renderWith({ mine, others, templates: { count: 7 } });

    expect(tabIds(html)).toEqual([
      'mine',
      'shared',
      'published',
      'templates',
      'others',
    ]);
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

    // Without other users' drafts it is the last tab.
    expect(tabIds(await renderWith({ mine, templates: { count: 0 } }))).toEqual(
      ['mine', 'shared', 'published', 'templates'],
    );
  });
});
