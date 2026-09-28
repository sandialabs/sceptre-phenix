// The Share dialog, the Share buttons and the lists of other users' drafts,
// rendered on the server: their names, descriptions and states as markup. Focus and keys need a browser,
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
  link: 'https://phenix.example/builder-beta?draft=alice%2Fd1',
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
    expect(html).toMatch(
      /data-testid="draft-shared-with-d1"[^>]*>\s*· Shared with bob and carol/,
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

  test('Shared with me lists the latest changed first, with owner, access and who changed it', async () => {
    const html = await render(BuilderDrafts, { shared });
    const panel = html.slice(
      html.indexOf('id="panel-shared"'),
      html.indexOf('id="panel-published"'),
    );

    expect(tag(html, 'data-testid="drafts-tab-shared"')).toBeTruthy();
    expect(html).toMatch(
      /data-testid="drafts-tab-shared"[^>]*>\s*Shared with me/,
    );
    expect(panel.indexOf('Newer')).toBeLessThan(panel.indexOf('Older'));
    expect(panel.indexOf('Older')).toBeLessThan(panel.indexOf('Locked'));
    expect(panel).toMatch(
      /Owner: bob[\s\S]*?data-testid="draft-access-s2"[^>]*>\s*· Can edit\s*<\/span>[\s\S]*?· Updated [^<]* by carol\s*</,
    );
    // Who changed it is said only when it was not the owner.
    expect(panel).toMatch(/data-testid="draft-access-s1"[^>]*>\s*· Can view/);
    expect(panel).not.toMatch(/· Updated [^<"]* by bob/);
    expect(panel).toMatch(
      /data-testid="draft-access-s3"[^>]*>\s*· Can edit \(your role allows viewing only\)/,
    );
    // Open is the only action on another user's draft, and it names the
    // owner, since two users' drafts can share a title.
    expect(tag(panel, 'data-testid="draft-open-s2"')).toMatch(
      /aria-label="Open Newer by bob, updated [^"]+"/,
    );
    expect(panel).not.toMatch(/draft-(share|delete)-s\d/);
  });

  test("with nothing shared, Shared with me says so, and there is no tab for other users' drafts", async () => {
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
  });
});
