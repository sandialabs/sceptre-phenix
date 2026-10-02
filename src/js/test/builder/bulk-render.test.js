// The row above a list whose items can be selected, the summary of what a
// bulk action left undone, and the user field, rendered on the server: what
// each says for the props it is given. Another list than the drafts' uses
// them as they are. Focus, keys and the mixed state of Select all need a
// browser, so they are left to builder-drafts.spec.js and
// builder-sharing.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import BuilderBulkBar from '@/components/builder/BuilderBulkBar.vue';
import BuilderBulkSummary from '@/components/builder/BuilderBulkSummary.vue';
import BuilderUserCombobox from '@/components/builder/BuilderUserCombobox.vue';

function render(component, props = {}, slots = {}) {
  return renderToString(
    createSSRApp({ render: () => h(component, props, slots) }),
  );
}

// The attributes of the one element the pattern finds.
function tag(html, pattern) {
  const found = html.match(new RegExp(`<[a-z]+\\b[^>]*${pattern}[^>]*>`));

  expect(found, pattern).not.toBeNull();

  return found[0];
}

describe('the row above a list', () => {
  const bar = (props, slots) =>
    render(
      BuilderBulkBar,
      {
        id: 'templates',
        label: 'Node Templates',
        count: 0,
        total: 4,
        ...props,
      },
      slots,
    );

  test('is a group named for the list, with ids and test ids made from its id', async () => {
    const html = await bar();
    const root = tag(html, 'data-testid="bulk-bar-templates"');

    expect(root).toContain('class="builder-bulk"');
    expect(root).toContain('role="group"');
    expect(root).toContain('aria-label="Bulk actions: Node Templates"');
    expect(tag(html, 'data-testid="bulk-all-templates"')).toContain(
      'type="checkbox"',
    );
    expect(html).toMatch(
      /<span id="bulk-count-templates"[^>]*data-testid="bulk-count-templates"[^>]*>0 of 4 selected<\/span>/,
    );
    expect(html).not.toContain('bulk-progress-');
  });

  test('Select all is checked only when every item is selected', async () => {
    const all = async (props) =>
      tag(await bar(props), 'data-testid="bulk-all-templates"');

    expect(await all({ count: 0, state: 'none' })).not.toContain('checked');
    // Some: the mixed state is a property the browser is given, not markup.
    expect(await all({ count: 2, state: 'some' })).not.toContain('checked');
    expect(await all({ count: 4, state: 'all' })).toContain('checked');
    expect(await bar({ count: 4, state: 'all' })).toContain(
      '>4 of 4 selected<',
    );
  });

  test('its buttons are the slot’s, between the count and the progress', async () => {
    const html = await bar(
      { running: { label: 'Deleting', done: 1, total: 3 } },
      { default: () => h('button', { id: 'act' }, 'Delete selected') },
    );

    expect(html.indexOf('bulk-count-templates')).toBeLessThan(
      html.indexOf('id="act"'),
    );
    expect(html.indexOf('id="act"')).toBeLessThan(
      html.indexOf('bulk-progress-templates'),
    );
  });

  test('while an action runs it says how far it is, and Select all waits', async () => {
    const html = await bar({
      count: 3,
      state: 'some',
      running: { label: 'Deleting', done: 1, total: 3 },
    });
    const progress = tag(html, 'data-testid="bulk-progress-templates"');

    expect(html).toMatch(
      /data-testid="bulk-progress-templates"[^>]*><span class="builder-toolbar__spinner" aria-hidden="true"[^>]*><\/span>\s*Deleting 1 of 3…\s*<\/span>/,
    );
    // Not a live region: the page says what the run came to.
    expect(progress).not.toMatch(/role=|aria-live/);
    expect(tag(html, 'data-testid="bulk-all-templates"')).toContain('disabled');

    // Nothing can be selected while an action runs on another list either.
    const waiting = await bar({ disabled: true });

    expect(tag(waiting, 'data-testid="bulk-all-templates"')).toContain(
      'disabled',
    );
    expect(waiting).not.toContain('bulk-progress-');
  });
});

describe('the summary of what a bulk action left undone', () => {
  const props = {
    heading: '2 of 3 drafts could not be shared. The other 1 was shared.',
    items: [
      { key: 'alice/d1', name: 'Lab', reason: 'Not attempted.' },
      { key: 'alice/d2', name: 'Core', reason: 'No user named zed.' },
    ],
  };

  test('is an alert named by its heading, which lists each item with why', async () => {
    const html = await render(BuilderBulkSummary, props);
    const root = tag(html, 'data-testid="bulk-summary"');
    const [, titleId] = root.match(/aria-labelledby="([^"]+)"/);

    expect(root).toContain('role="alert"');
    // It takes focus, without being a tab stop.
    expect(root).toContain('tabindex="-1"');
    expect(root).toContain('builder-bulk-summary');
    expect(html).toMatch(
      new RegExp(
        `<h3 id="${titleId}"[^>]*>2 of 3 drafts could not be shared\\. The other 1 was shared\\.</h3>`,
      ),
    );
    expect(
      [...html.matchAll(/<li[^>]*>([^<]*)<\/li>/g)].map(([, text]) =>
        text.trim(),
      ),
    ).toEqual(['Lab: Not attempted.', 'Core: No user named zed.']);
    expect(html).toMatch(
      /data-testid="bulk-summary-dismiss"[^>]*>\s*Dismiss\s*<\/button>/,
    );
  });

  test('has no Dismiss where the dialog it is in has its own way out', async () => {
    const html = await render(BuilderBulkSummary, {
      ...props,
      dismissible: false,
    });

    expect(html).toContain('data-testid="bulk-summary"');
    expect(html).not.toContain('bulk-summary-dismiss');
    expect(html).not.toContain('<button');
  });

  test('two summaries on one page have headings of their own', async () => {
    const html = await renderToString(
      createSSRApp({
        render: () =>
          h('div', [
            h(BuilderBulkSummary, props),
            h(BuilderBulkSummary, props),
          ]),
      }),
    );
    const ids = [...html.matchAll(/<h3 id="([^"]+)"/g)].map(([, id]) => id);

    expect(ids).toHaveLength(2);
    expect(new Set(ids).size).toBe(2);
  });
});

describe('the user field', () => {
  const field = (props) =>
    render(BuilderUserCombobox, { id: 'template-share-user', ...props });

  test('names its elements for its id, and is an editable combobox over a list of users', async () => {
    const html = await field({ modelValue: 'bo', users: null });
    // " id=", so a data-testid of the same value is not taken for it.
    const input = tag(html, ' id="template-share-user"');
    const list = tag(html, ' id="template-share-user-options"');
    const toggle = tag(html, 'data-testid="template-share-user-toggle"');

    expect(input).toMatch(/^<input/);
    expect(input).toContain('value="bo"');
    expect(input).toContain('role="combobox"');
    expect(input).toContain('aria-autocomplete="list"');
    expect(input).toContain('aria-expanded="false"');
    expect(input).toContain('aria-controls="template-share-user-options"');
    expect(input).toContain('data-testid="template-share-user"');
    expect(input).toContain('autocomplete="off"');
    expect(input).not.toContain('aria-invalid');
    expect(input).not.toContain('aria-describedby');
    expect(input).not.toContain('aria-activedescendant');
    expect(input).not.toContain('disabled');
    expect(list).toContain('role="listbox"');
    expect(list).toContain('aria-label="Users"');
    expect(list).toContain('data-testid="template-share-user-options"');
    // The button opens the list; the keys do the same from the field.
    expect(toggle).toContain('tabindex="-1"');
    expect(toggle).toContain('aria-label="Users"');
    expect(toggle).toContain('aria-controls="template-share-user-options"');
    // How many users match is said in a region of its own.
    expect(tag(html, 'data-testid="template-share-user-matches"')).toContain(
      'role="status"',
    );
  });

  test('lists the users offered, less those excluded, each named "Name (username)"', async () => {
    const html = await field({
      users: [
        { username: 'carol', name: '' },
        { username: 'bob', name: 'Bob Builder' },
        { username: 'alice', name: 'Alice' },
      ],
      exclude: ['alice'],
    });
    const options = [
      ...html.matchAll(
        /<li id="(template-share-user-option-\d)"[^>]*role="option"[^>]*data-testid="([^"]+)"[^>]*>([^<]*)<\/li>/g,
      ),
    ].map(([, id, testid, text]) => [id, testid, text.trim()]);

    expect(options).toEqual([
      [
        'template-share-user-option-0',
        'template-share-user-option-bob',
        'Bob Builder (bob)',
      ],
      [
        'template-share-user-option-1',
        'template-share-user-option-carol',
        'carol',
      ],
    ]);
  });

  test('is marked, described and disabled as its form says', async () => {
    const input = tag(
      await field({
        invalid: true,
        disabled: true,
        describedby: 'note error',
      }),
      'id="template-share-user"',
    );

    expect(input).toContain('aria-invalid="true"');
    expect(input).toContain('aria-describedby="note error"');
    expect(input).toContain('disabled');
    expect(
      tag(
        await field({ disabled: true }),
        'data-testid="template-share-user-toggle"',
      ),
    ).toContain('disabled');
  });
});
