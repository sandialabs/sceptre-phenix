// The Share dialog, the Share buttons, the lists of other users' drafts and
// the Delete of published topologies, rendered on the server: their names, descriptions and states as markup. Focus and keys need a browser,
// so they are left to the Playwright specs.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import BuilderDrafts from '@/components/builder/BuilderDrafts.vue';
import BuilderToolbar from '@/components/builder/BuilderToolbar.vue';
import BulkShareDialog from '@/components/builder/dialogs/BulkShareDialog.vue';
import ShareDialog from '@/components/builder/dialogs/ShareDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

async function render(component, props = {}, setup = () => {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });

  app.use(pinia);
  setup(useBuilderStore(pinia));

  return renderToString(app);
}

// The attributes of the one element the pattern finds.
function tag(html, pattern) {
  const found = html.match(new RegExp(`<[a-z]+\\b[^>]*${pattern}[^>]*>`));

  expect(found, pattern).not.toBeNull();

  return found[0];
}

const target = {
  owner: 'alice',
  id: 'd1',
  name: 'Network lab',
  link: 'https://phenix.example/builder?draft=alice%2Fd1',
  shares: [
    { user: 'bob', access: 'edit' },
    { user: 'carol', access: 'view', stale: true },
  ],
};

describe('the Share dialog', () => {
  test('is named for the draft and described by what sharing gives', async () => {
    const html = await render(ShareDialog, { target });
    const dialog = tag(html, 'data-testid="share-dialog"');

    expect(dialog).toMatch(/^<dialog/);
    expect(html).toMatch(/<h2 id="share-dialog-title"[^>]*>Share Network lab</);
    expect(dialog).toContain('aria-labelledby="share-dialog-title"');
    expect(dialog).toContain('aria-describedby="share-intro"');
    expect(html).toMatch(/<p id="share-intro"[^>]*>\s*People you add find/);
  });

  test('lists the owner first, then each person with their own controls', async () => {
    const html = await render(ShareDialog, { target });
    const people = html.slice(html.indexOf('data-testid="share-people"'));

    expect(people.indexOf('alice (you)')).toBeLessThan(people.indexOf('bob'));
    expect(people).toMatch(/alice \(you\)<\/span><span[^>]*>Owner</);

    const access = tag(html, 'aria-label="Access for bob"');
    const remove = tag(html, 'aria-label="Remove bob"');

    expect(access).toMatch(/^<select/);
    expect(access).not.toContain('aria-describedby');
    expect(remove).toMatch(/^<button/);
    expect(html).toMatch(/<option value="edit" selected[^>]*>\s*Can edit/);
    // The list is not read yet: nothing can change meanwhile.
    expect(access).toContain('disabled');
    expect(tag(html, 'data-testid="share-save"')).toContain(
      'aria-disabled="true"',
    );
    expect(tag(html, 'data-testid="share-people"')).toContain(
      'aria-busy="true"',
    );
    expect(html).toContain('Loading who has access…');
  });

  test('a removed account starts marked removed, and cannot be kept', async () => {
    const html = await render(ShareDialog, { target });
    const keep = tag(html, 'aria-label="Keep carol"');

    expect(keep).toContain('aria-disabled="true"');
    expect(keep).toContain('aria-describedby="share-row-1-note"');
    expect(html).toMatch(/id="share-row-1-note"[^>]*>\s*Account removed/);
    expect(tag(html, 'data-testid="share-row-carol"')).toContain('is-removed');
    expect(tag(html, 'aria-label="Access for carol"')).toContain('disabled');
  });

  test('its regions are there, empty, before anything is said', async () => {
    const html = await render(ShareDialog, { target });

    expect(tag(html, 'data-testid="share-alert"')).toContain('role="alert"');
    // Enter in the User field keeps focus there, so its error is an alert.
    expect(tag(html, 'data-testid="share-user-error"')).toContain(
      'role="alert"',
    );
    expect(tag(html, 'data-testid="share-status"')).toContain('role="status"');
    expect(tag(html, 'data-testid="share-copy-status"')).toContain(
      'role="status"',
    );
    expect(html).toMatch(/<label for="share-access"[^>]*>Access</);
    expect(html).toMatch(/<option value="view"[^>]*>Can view</);
  });

  test('people are added from a list of users, which says while it loads', async () => {
    const html = await render(ShareDialog, { target });
    const field = tag(html, 'id="share-user"');
    const toggle = tag(html, 'data-testid="share-user-toggle"');
    const list = tag(html, 'id="share-user-options"');

    expect(html).toMatch(/<label for="share-user"[^>]*>User</);
    expect(field).toContain('role="combobox"');
    expect(field).toContain('aria-autocomplete="list"');
    expect(field).toContain('aria-expanded="false"');
    expect(field).toContain('aria-controls="share-user-options"');
    expect(field).toContain('aria-describedby="share-users-note"');
    expect(field).toContain('autocapitalize="none"');
    expect(field).toContain('spellcheck="false"');
    // The button opens the list; the keys do the same from the field.
    expect(toggle).toMatch(/^<button/);
    expect(toggle).toContain('tabindex="-1"');
    expect(toggle).toContain('aria-label="Users"');
    expect(toggle).toContain('aria-expanded="false"');
    expect(list).toContain('role="listbox"');
    expect(list).toContain('aria-label="Users"');
    expect(tag(html, 'data-testid="share-users-note"')).toContain(
      'role="status"',
    );
    expect(html).toMatch(
      /data-testid="share-users-note"[^>]*>[\s\S]*?Loading users…/,
    );
  });
});

describe('the dialog that shares several drafts', () => {
  const items = [
    { id: 'd1', owner: 'alice', title: 'Network lab', canShare: true },
    { id: 'd2', owner: 'alice', title: 'Plant', canShare: true },
  ];

  test('is named for how many drafts, and described by what it does to who has access', async () => {
    const html = await render(BulkShareDialog, { items });
    const dialog = tag(html, 'data-testid="bulk-share-dialog"');

    expect(dialog).toMatch(/^<dialog/);
    expect(html).toMatch(/<h2 id="bulk-share-title"[^>]*>Share 2 drafts</);
    expect(dialog).toContain('aria-labelledby="bulk-share-title"');
    expect(dialog).toContain('aria-describedby="bulk-share-intro"');
    expect(
      html
        .match(/<p id="bulk-share-intro"[^>]*>([\s\S]*?)<\/p>/)[1]
        .replace(/\s+/g, ' ')
        .trim(),
    ).toBe(
      'The people you add get access to every selected draft, with the access you choose. ' +
        'People who already have access keep it. If someone you add is already on a draft, ' +
        'their access becomes the one you choose here. No one is removed. ' +
        'They find the drafts under Shared Drafts.',
    );
    expect(html).not.toContain('bulk-share-left-out');

    expect(await render(BulkShareDialog, { items: [items[0]] })).toMatch(
      /<h2 id="bulk-share-title"[^>]*>Share 1 draft</,
    );
  });

  test('says how many damaged drafts of the selection are left out', async () => {
    const note = async (leftOut) =>
      (await render(BulkShareDialog, { items, leftOut }))
        .match(/data-testid="bulk-share-left-out"[^>]*>([\s\S]*?)<\/p>/)[1]
        .replace(/\s+/g, ' ')
        .trim();

    expect(await note(1)).toBe(
      '1 damaged draft in your selection cannot be shared and is left out.',
    );
    expect(await note(3)).toBe(
      '3 damaged drafts in your selection cannot be shared and are left out.',
    );
  });

  test('has the user field of the Share dialog, one access for everyone, and an empty list', async () => {
    const html = await render(BulkShareDialog, { items });
    const field = tag(html, 'id="bulk-share-user"');

    expect(html).toMatch(/<label for="bulk-share-user"[^>]*>User</);
    expect(field).toContain('role="combobox"');
    expect(field).toContain('aria-autocomplete="list"');
    expect(field).toContain('aria-expanded="false"');
    expect(field).toContain('aria-controls="bulk-share-user-options"');
    expect(field).toContain('data-testid="bulk-share-user"');
    // The users are being read: the note says so, and describes the field.
    expect(field).toContain('aria-describedby="bulk-share-users-note"');
    expect(field).not.toContain('aria-invalid');
    expect(tag(html, 'data-testid="bulk-share-user-toggle"')).toContain(
      'aria-controls="bulk-share-user-options"',
    );
    expect(tag(html, 'id="bulk-share-user-options"')).toContain(
      'role="listbox"',
    );
    expect(tag(html, 'data-testid="bulk-share-users-note"')).toContain(
      'role="status"',
    );
    expect(html).toMatch(
      /data-testid="bulk-share-users-note"[^>]*>[\s\S]*?Loading users…/,
    );
    expect(tag(html, 'data-testid="bulk-share-user-error"')).toContain(
      'role="alert"',
    );
    expect(html).toMatch(/data-testid="bulk-share-add"[^>]*>[\s\S]*?Add\s*</);

    expect(html).toMatch(/<label for="bulk-share-access"[^>]*>Access</);
    expect(html).toMatch(
      /<select id="bulk-share-access"[\s\S]*?<option value="view"[^>]*>Can view<\/option><option value="edit"[^>]*>Can edit<\/option>/,
    );
    expect(html).toMatch(
      /<h3 id="bulk-share-people-title"[^>]*>People to add</,
    );
    expect(html).toMatch(/data-testid="bulk-share-empty"[^>]*>\s*No one yet\./);
    expect(html).not.toContain('bulk-summary');
  });

  test('its primary button says how many drafts, and waits for someone to add', async () => {
    const html = await render(BulkShareDialog, { items });
    const submit = tag(html, 'data-testid="bulk-share-submit"');

    expect(submit).toContain('builder-button--primary');
    expect(submit).toContain('aria-disabled="true"');
    expect(html).toMatch(
      /data-testid="bulk-share-submit"[^>]*>\s*Share 2 drafts\s*<\/button>/,
    );
    expect(html).toMatch(
      /data-testid="bulk-share-cancel"[^>]*>\s*Cancel\s*<\/button>/,
    );
    expect(tag(html, 'data-testid="bulk-share-cancel"')).not.toContain(
      'aria-disabled',
    );
    expect(html).not.toContain('bulk-share-close');
    // The status line is there, empty, before the drafts are shared.
    expect(html).toMatch(
      /<p[^>]*role="status"[^>]*data-testid="bulk-share-status"[^>]*>(<!--[^>]*-->)*<\/p>/,
    );
  });
});

describe('the Share buttons', () => {
  const mine = [
    {
      id: 'd1',
      owner: 'alice',
      title: 'Network lab',
      canShare: true,
      shares: [
        { user: 'bob', access: 'edit' },
        { user: 'carol', access: 'view' },
      ],
    },
    { id: 'd2', owner: 'alice', title: 'Plain', canShare: false },
  ];

  test('a draft of mine the server lets me share has Share, and says with whom', async () => {
    const html = await render(BuilderDrafts, {
      mine,
      shared: [{ id: 's1', owner: 'bob', title: 'Theirs', canShare: true }],
    });
    const share = tag(html, 'data-testid="draft-share-d1"');

    expect(share).toContain('aria-label="Share Network lab"');
    expect(share).toContain('aria-haspopup="dialog"');
    // On a line of its own, which starts with no separator.
    expect(tag(html, 'data-testid="draft-shared-with-d1"')).toMatch(/^<p /);
    expect(html).toMatch(
      /data-testid="draft-shared-with-d1"[^>]*>\s*Shared with bob and carol/,
    );
    expect(html).not.toContain('draft-share-d2');
    expect(html).not.toContain('draft-share-s1');
  });

  test('the toolbar has Share after Publish for the owner, and names the owner to others', async () => {
    const owner = await render(BuilderToolbar, {}, (store) => {
      store.canShare = true;
      store.access = 'owner';
      store.shares = [{ user: 'bob', access: 'edit' }];
    });
    const share = tag(owner, 'data-testid="toolbar-share"');

    expect(owner.indexOf('toolbar-share')).toBeGreaterThan(
      owner.indexOf('toolbar-publish'),
    );
    expect(share).not.toContain('aria-disabled');
    expect(share).toContain('aria-describedby="toolbar-tip-share"');
    expect(owner).toMatch(/id="toolbar-tip-share"[^>]*>\s*Shared with bob/);

    const recipient = await render(BuilderToolbar, {}, (store) => {
      store.owner = 'bob';
      store.access = 'edit';
      store.via = 'share';
    });

    expect(tag(recipient, 'data-testid="toolbar-share"')).toContain(
      'aria-disabled="true"',
    );
    expect(recipient).toMatch(
      /id="toolbar-tip-share"[^>]*>\s*Only bob can change who has access/,
    );

    const other = await render(BuilderToolbar, {}, (store) => {
      store.owner = 'carol';
      store.access = 'view';
      store.via = 'role';
    });

    expect(other).not.toContain('toolbar-share');
  });
});

describe('the toolbar’s Exp', () => {
  test('is last of the ways out, only while the diagram was published with an experiment', async () => {
    const none = await render(BuilderToolbar, {}, (store) => {
      store.canShare = true;
      store.access = 'owner';
    });

    expect(none).not.toContain('toolbar-experiment');

    const some = await render(BuilderToolbar, {}, (store) => {
      store.canShare = true;
      store.access = 'owner';
      store.experiment = 'lab-exp';
    });
    const exp = tag(some, 'data-testid="toolbar-experiment"');

    expect(exp).toMatch(/^<button/);
    // After Share, before the next group's Minimap.
    expect(some.indexOf('toolbar-experiment')).toBeGreaterThan(
      some.indexOf('toolbar-share'),
    );
    expect(some.indexOf('toolbar-experiment')).toBeLessThan(
      some.indexOf('toolbar-minimap'),
    );
    // Named for the experiment, starting with its visible text; it leaves
    // the Builder, so it announces no dialog.
    expect(exp).toContain('aria-label="Exp: open experiment lab-exp"');
    expect(exp).not.toContain('aria-haspopup');
    expect(exp).not.toContain('aria-disabled');
    // No keys by default, so nothing more describes it.
    expect(exp).not.toContain('aria-describedby');
    expect(some).toMatch(
      /data-testid="toolbar-experiment"[^>]*>\s*<svg[^>]*builder-icon--experiment[\s\S]*?<\/svg>\s*Exp\s*<\/button>/,
    );
  });

  test('shows for a published diagram opened read only, from its listed row', async () => {
    const viewed = (documents) =>
      render(BuilderToolbar, {}, (store) => {
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

describe('drafts other users shared or let me see', () => {
  const shared = [
    {
      id: 's1',
      owner: 'bob',
      title: 'Older',
      access: 'view',
      via: 'share',
      updated: '2026-09-27T09:00:00Z',
      lastModifiedBy: 'bob',
    },
    {
      id: 's2',
      owner: 'bob',
      title: 'Newer',
      access: 'edit',
      via: 'share',
      updated: '2026-09-27T10:00:00Z',
      lastModifiedBy: 'carol',
    },
    {
      id: 's3',
      owner: 'dave',
      title: 'Locked',
      access: 'edit',
      readOnly: true,
      via: 'share',
      updated: '2026-09-27T08:00:00Z',
    },
  ];

  test('Shared Drafts lists the latest changed first, with owner, access and who changed it', async () => {
    const html = await render(BuilderDrafts, { shared });
    const panel = html.slice(
      html.indexOf('id="panel-shared"'),
      html.indexOf('id="panel-published"'),
    );

    expect(tag(html, 'data-testid="drafts-tab-shared"')).toBeTruthy();
    expect(html).toMatch(
      /data-testid="drafts-tab-shared"[^>]*>\s*Shared Drafts/,
    );
    expect(html).not.toContain('Shared with me');
    expect(panel.indexOf('Newer')).toBeLessThan(panel.indexOf('Older'));
    expect(panel.indexOf('Older')).toBeLessThan(panel.indexOf('Locked'));
    // The owner and the access on one line, the time on the next.
    expect(panel).toMatch(
      /<p[^>]*>\s*Owner: bob\s*<span[^>]*data-testid="draft-access-s2"[^>]*>\s*· Can edit\s*<\/span>\s*<\/p>\s*<p[^>]*data-testid="card-time-s2"[^>]*>\s*Updated [^<]* by carol\s*<\/p>/,
    );
    // Who changed it is said only when it was not the owner.
    expect(panel).toMatch(/data-testid="draft-access-s1"[^>]*>\s*· Can view/);
    expect(panel).toMatch(/data-testid="card-time-s1"[^>]*>\s*Updated [^<]*</);
    expect(panel).not.toMatch(/Updated [^<"]* by bob/);
    expect(panel).not.toContain('· Updated');
    expect(panel).toMatch(
      /data-testid="draft-access-s3"[^>]*>\s*· Can edit \(your role allows viewing only\)/,
    );
    // A shared draft has no Share or Delete. Its buttons name the owner,
    // since two users' drafts can share a title.
    expect(tag(panel, 'data-testid="draft-open-s2"')).toMatch(
      /aria-label="Open Newer by bob, updated [^"]+"/,
    );
    expect(panel).not.toMatch(/draft-(share|delete)-s\d/);
    // Publish is on the one the user may change: not one shared to view,
    // or one their role lets them only view.
    expect(tag(panel, 'data-testid="draft-publish-s2"')).toMatch(
      /aria-label="Publish Newer by bob, updated [^"]+"/,
    );
    expect(panel).not.toContain('draft-publish-s1');
    expect(panel).not.toContain('draft-publish-s3');
  });

  test("with nothing shared, Shared Drafts says so, and there is no tab for other users' drafts", async () => {
    const html = await render(BuilderDrafts, {});

    expect(html).toContain('No one has shared a draft with you yet.');
    expect(html).not.toContain('drafts-tab-others');
  });

  test("other users' drafts the role lets me see, and their damaged ones, have their own tab, last", async () => {
    const html = await render(BuilderDrafts, {
      others: [
        {
          id: 'r1',
          owner: 'erin',
          title: 'Theirs',
          access: 'view',
          via: 'role',
        },
      ],
      damaged: {
        mine: [],
        others: [{ id: 'x1', owner: 'erin', damaged: true, canDelete: true }],
      },
    });
    const panel = html.slice(html.indexOf('id="panel-others"'));

    expect(html.indexOf('drafts-tab-others')).toBeGreaterThan(
      html.indexOf('drafts-tab-published'),
    );
    expect(html).toMatch(
      /data-testid="drafts-tab-others"[^>]*>\s*Other users(&#39;|') drafts/,
    );
    expect(panel).toContain('data-testid="draft-open-r1"');
    expect(panel).toContain('data-testid="draft-damaged-x1"');
    expect(tag(panel, 'data-testid="draft-delete-x1"')).toMatch(
      /aria-label="Delete [^"]* by erin"/,
    );
    // A readable one has Delete only when its row says the user may
    // delete it (see drafts-render.test.js).
    expect(panel).not.toContain('draft-delete-r1');
  });
});

describe('published diagrams', () => {
  const published = [
    {
      id: 'p1',
      kind: 'Topology',
      target: 'lab',
      createdAt: '2026-09-27T09:00:00Z',
    },
    {
      id: 'p2',
      kind: 'Experiment',
      target: 'run',
      createdAt: '2026-09-27T10:00:00Z',
    },
  ];

  test('a published topology has Delete when the role may delete configs, and says while it deletes', async () => {
    const html = await render(BuilderDrafts, { published });
    const panel = html.slice(html.indexOf('id="panel-published"'));
    const remove = tag(panel, 'data-testid="published-delete-p1"');

    expect(remove).toMatch(/aria-label="Delete lab, published [^"]+"/);
    expect(remove).toContain('aria-haspopup="dialog"');
    expect(remove).not.toContain('aria-disabled');
    // A published experiment is deleted from the Experiments page.
    expect(panel).not.toContain('published-delete-p2');

    const deleting = tag(
      await render(BuilderDrafts, { published, deletingPublished: ['p1'] }),
      'data-testid="published-delete-p1"',
    );

    expect(deleting).toMatch(/aria-label="Deleting lab, published [^"]+"/);
    expect(deleting).toContain('aria-disabled="true"');
    expect(deleting).toContain('aria-busy="true"');

    expect(
      await render(BuilderDrafts, { published, canDelete: false }),
    ).not.toContain('published-delete-');
  });

  // A topology whose diagram is read from the Builder file it names is
  // listed by a handle, with the file's path and no published document.
  test('a topology read from its Builder file is tagged File, says where from, and has no Delete', async () => {
    const file = {
      source: 'file',
      id: 'file/plant',
      kind: 'Topology',
      target: 'plant',
      config: 'Topology/plant',
      path: '/phenix/topologies/plant/plant.builder.json',
    };
    const html = await render(BuilderDrafts, {
      published: [{ ...published[0], source: 'store' }, file],
    });
    const panel = html.slice(html.indexOf('id="panel-published"'));
    const card = panel.slice(panel.indexOf('>plant'));

    // The tag is a word in the card's heading, not a color or an icon.
    expect(panel).toMatch(
      /<h2[^>]*>\s*plant\s*<span[^>]*data-testid="published-file-file\/plant"[^>]*>File<\/span>/,
    );
    expect(tag(panel, 'data-testid="published-path-file/plant"')).toMatch(
      /^<p /,
    );
    expect(card).toMatch(
      />\s*Read from \/phenix\/topologies\/plant\/plant\.builder\.json\s*</,
    );
    expect(card).not.toContain('Published');
    // Open names the file, as a published diagram's names its time.
    expect(tag(panel, 'data-testid="draft-open-file/plant"')).toContain(
      'aria-label="Open plant, read from /phenix/topologies/plant/plant.builder.json"',
    );
    // Nothing was published, so nothing is deleted here; the published
    // topology beside it keeps its Delete and has no tag.
    expect(panel).not.toContain('published-delete-file/plant');
    expect(panel).toContain('data-testid="published-delete-p1"');
    expect(panel).not.toContain('published-file-p1');
    expect(panel.match(/builder-drafts__tag/g)).toHaveLength(1);
  });
});
