// The header's counts, the Settings dialog's zoom choices and the Auto-group
// by name pattern dialog, rendered on the server: their markup, names and
// descriptions. Pressing a count, changing the percentage and running a
// pattern need a browser, so they are left to the Playwright specs.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import BuilderCounts from '@/components/builder/BuilderCounts.vue';
import BuilderSettings from '@/components/builder/BuilderSettings.vue';
import GroupPatternDialog from '@/components/builder/dialogs/GroupPatternDialog.vue';
import { GROUP_PATTERN_STORAGE_KEY } from '@/builder/grouping.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';

import { memoryStorage, sampleDocument } from './fixtures.js';

async function render(
  component,
  { doc = sampleDocument().doc, selection } = {},
) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component) });

  app.use(pinia);

  const store = useBuilderStore(pinia);

  store.doc = doc;
  if (selection) {
    store.selection = selection;
  }

  return renderToString(app);
}

// What an element holds, as text: comments and tags out, white space as one
// space.
function textOf(html) {
  return html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]*>/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

// The value of an attribute of an opening tag, or null without it.
function attribute(tag, name) {
  return tag.match(new RegExp(`\\s${name}="([^"]*)"`))?.[1] ?? null;
}

// The opening tag with the test id.
function tagOf(html, testid) {
  return html.match(
    new RegExp(`<[a-z]+\\b[^>]*data-testid="${testid}"[^>]*>`),
  )?.[0];
}

describe('the header’s counts', () => {
  test('are buttons in a list whose text is the counts alone', async () => {
    const html = await render(BuilderCounts);
    const [, list] = html.match(
      /<ul\b[^>]*data-testid="builder-summary"[^>]*>([\s\S]*?)<\/ul>/,
    );
    const items = [...list.matchAll(/<li\b([^>]*)>([\s\S]*?)<\/li>/g)];

    // The helpers of the Playwright specs read these texts.
    expect(textOf(list)).toBe(
      '2 devices, 1 switch, 1 network, 1 connection, 0 groups, 0 notes',
    );
    expect(items.map(([, , inner]) => textOf(inner))).toEqual([
      '2 devices,',
      '1 switch,',
      '1 network,',
      '1 connection,',
      '0 groups,',
      '0 notes',
    ]);
    expect(
      items.map(([, attributes]) => attribute(attributes, 'data-kind')),
    ).toEqual(['devices', 'switches', 'networks', 'links', 'groups', 'notes']);

    const buttons = items.map(
      ([, , inner]) =>
        inner.match(/<button\b([^>]*)>([\s\S]*?)<\/button>/) || [],
    );

    // Each button is named by its count in words, without the comma.
    expect(buttons.map(([, , inner]) => textOf(inner))).toEqual([
      '2 devices',
      '1 switch',
      '1 network',
      '1 connection',
      '0 groups',
      '0 notes',
    ]);
    expect(
      buttons.map(([, attributes]) => attribute(attributes, 'data-testid')),
    ).toEqual([
      'count-devices',
      'count-switches',
      'count-networks',
      'count-links',
      'count-groups',
      'count-notes',
    ]);
    expect(
      buttons.every(
        ([, attributes]) => attribute(attributes, 'type') === 'button',
      ),
    ).toBe(true);
    // One Tab stop: the first count, until another is focused.
    expect(
      buttons.map(([, attributes]) => attribute(attributes, 'tabindex')),
    ).toEqual(['0', '-1', '-1', '-1', '-1', '-1']);
    // A count of none is aria-disabled, and has no description.
    expect(
      buttons.map(([, attributes]) => attribute(attributes, 'aria-disabled')),
    ).toEqual([null, null, null, null, 'true', 'true']);
    expect(
      buttons.map(([, attributes]) =>
        attribute(attributes, 'aria-describedby'),
      ),
    ).toEqual([
      'count-tip-devices',
      'count-tip-switches',
      'count-tip-networks',
      'count-tip-links',
      null,
      null,
    ]);
    // The icons are a fifth larger than the 14 pixels they were.
    for (const [, , inner] of buttons) {
      expect(inner).toMatch(/<svg\b[^>]*width="17"[^>]*height="17"/);
    }
  });

  test('describe what each count selects, outside the list', async () => {
    const html = await render(BuilderCounts);
    const outside = html.replace(/<ul\b[\s\S]*?<\/ul>/, '');
    const tips = Object.fromEntries(
      [
        ...outside.matchAll(
          /<span\b[^>]*id="(count-tip-[a-z]+)"[^>]*hidden[^>]*>([\s\S]*?)<\/span>/g,
        ),
      ].map(([, id, inner]) => [id, textOf(inner)]),
    );

    expect(tips).toEqual({
      'count-tip-devices': 'Select all 2 devices',
      'count-tip-switches': 'Select the 1 switch',
      'count-tip-networks': 'Select the switches of the 1 network',
      'count-tip-links': 'Select the 1 connection',
    });
  });
});

describe('the Settings dialog', () => {
  beforeEach(() => {
    resetSettings(null);
  });

  afterEach(() => {
    resetSettings(null);
  });

  test('names the layout setting Default layout', async () => {
    const html = await render(BuilderSettings);

    expect(html).toMatch(
      /<label for="settings-layout"[^>]*>\s*Default layout\s*<\/label>/,
    );
    expect(html).not.toContain('Layout for drafts without one');
  });

  test('offers Show node notes, on until it is turned off, with its hint', async () => {
    const html = await render(BuilderSettings);
    const toggle = tagOf(html, 'settings-node-notes');

    expect(attribute(toggle, 'role')).toBe('switch');
    expect(attribute(toggle, 'aria-checked')).toBe('true');
    expect(attribute(toggle, 'aria-describedby')).toBe(
      'settings-node-notes-hint',
    );
    expect(textOf(html)).toContain('Show node notes');
    expect(html).toMatch(
      /<p id="settings-node-notes-hint"[^>]*>\s*The notes of devices and switches, below each node on the canvas/,
    );

    setSetting('showNodeNotes', false, null);
    expect(
      attribute(
        tagOf(await render(BuilderSettings), 'settings-node-notes'),
        'aria-checked',
      ),
    ).toBe('false');
  });

  test('offers 100%, a fit, and a custom percentage for the zoom a diagram opens with', async () => {
    const html = await render(BuilderSettings);
    const [, group] = html.match(
      /<legend[^>]*>Zoom when a diagram opens<\/legend>([\s\S]*?)<\/fieldset>/,
    );
    const radios = [...group.matchAll(/<input\b[^>]*type="radio"[^>]*>/g)].map(
      ([tag]) => tag,
    );

    expect(radios.map((tag) => attribute(tag, 'value'))).toEqual([
      'actual',
      'fit',
      'custom',
    ]);
    expect(radios.map((tag) => attribute(tag, 'data-testid'))).toEqual([
      'settings-zoom-actual',
      'settings-zoom-fit',
      'settings-zoom-custom',
    ]);
    expect(radios.map((tag) => /\schecked\b/.test(tag))).toEqual([
      true,
      false,
      false,
    ]);
    expect(
      [...group.matchAll(/<label\b[^>]*>([\s\S]*?)<\/label>/g)].map(
        ([, inner]) => textOf(inner),
      ),
    ).toEqual(['100%', 'Fit the whole diagram in view', 'Custom']);

    const field = tagOf(group, 'settings-zoom-percent');

    expect(field).toMatch(/^<input\b/);
    expect(attribute(field, 'id')).toBe('settings-zoom-percent');
    expect(attribute(field, 'type')).toBe('number');
    expect(attribute(field, 'min')).toBe('20');
    expect(attribute(field, 'max')).toBe('200');
    expect(attribute(field, 'step')).toBe('5');
    expect(attribute(field, 'inputmode')).toBe('numeric');
    expect(attribute(field, 'value')).toBe('100');
    expect(attribute(field, 'aria-label')).toBe('Custom zoom, percent');
    expect(attribute(field, 'aria-describedby')).toBe(
      'settings-zoom-percent-hint settings-zoom-hint',
    );
    expect(attribute(field, 'aria-invalid')).toBeNull();
    // Always enabled: typing a percentage chooses Custom.
    expect(field).not.toMatch(/\sdisabled\b/);

    expect(group).toMatch(
      /<p id="settings-zoom-percent-hint"[^>]*>\s*From 20 to 200\.\s*<\/p>/,
    );
    expect(group).toMatch(
      /<p id="settings-zoom-hint"[^>]*>\s*Reset view goes back to it too\.\s*<\/p>/,
    );
    // The message is there, empty, from the start.
    expect(group).toMatch(
      /<p id="settings-zoom-percent-error"[^>]*role="alert"[^>]*>(<!--[\s\S]*?-->)*<\/p>/,
    );
  });

  test('shows a custom zoom that is kept', async () => {
    setSetting('openZoom', 'custom', null);
    setSetting('openZoomPercent', 75, null);

    const html = await render(BuilderSettings);

    expect(tagOf(html, 'settings-zoom-custom')).toMatch(/\schecked\b/);
    expect(tagOf(html, 'settings-zoom-actual')).not.toMatch(/\schecked\b/);
    expect(attribute(tagOf(html, 'settings-zoom-percent'), 'value')).toBe('75');
  });
});

describe('the Auto-group by name pattern dialog', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('asks for the pattern, and says what it does with it', async () => {
    const html = await render(GroupPatternDialog);
    const field = tagOf(html, 'group-pattern-field');

    expect(html).toMatch(
      /<h2 id="group-pattern-dialog-title"[^>]*>Auto-group by name pattern<\/h2>/,
    );
    expect(
      textOf(
        html.match(
          /<p\b[^>]*data-testid="group-pattern-intro"[^>]*>([\s\S]*?)<\/p>/,
        )[1],
      ),
    ).toBe('Groups the ungrouped devices and switches whose names match.');
    expect(html).toMatch(
      /<label for="group-pattern"[^>]*>Name pattern<\/label>/,
    );
    expect(attribute(field, 'id')).toBe('group-pattern');
    expect(attribute(field, 'type')).toBe('text');
    expect(attribute(field, 'maxlength')).toBe('200');
    expect(attribute(field, 'autocomplete')).toBe('off');
    expect(attribute(field, 'autocapitalize')).toBe('off');
    expect(attribute(field, 'spellcheck')).toBe('false');
    expect(attribute(field, 'aria-describedby')).toBe('group-pattern-hint');
    expect(attribute(field, 'aria-invalid')).toBeNull();
    // Nothing is kept yet: the field is empty.
    expect(attribute(field, 'value') || '').toBe('');
    expect(
      textOf(html.match(/<p id="group-pattern-hint"[^>]*>([\s\S]*?)<\/p>/)[1]),
    ).toBe(
      'A regular expression, matched against each name, ignoring case. Names with the same matched text go in one group, named after that text. With parentheses, the text of the first pair is used. For example, ^[a-z]+ puts web-01 and web-02 in a group named web.',
    );
    expect(html).toMatch(
      /<p id="group-pattern-error"[^>]*role="alert"[^>]*>(<!--[\s\S]*?-->)*<\/p>/,
    );

    const cancel = html.match(
      /<button\b[^>]*data-testid="group-pattern-cancel"[^>]*>([\s\S]*?)<\/button>/,
    );
    const submit = html.match(
      /<button\b([^>]*data-testid="group-pattern-submit"[^>]*)>([\s\S]*?)<\/button>/,
    );

    expect(textOf(cancel[1])).toBe('Cancel');
    expect(textOf(submit[2])).toBe('Group');
    expect(attribute(submit[1], 'type')).toBe('submit');
    expect(attribute(submit[1], 'aria-busy')).toBeNull();
    expect(attribute(submit[1], 'aria-disabled')).toBeNull();
  });

  test('says so when only the selected nodes are grouped', async () => {
    const { doc, alpha } = sampleDocument();
    const html = await render(GroupPatternDialog, {
      doc,
      selection: { nodes: [alpha.id], edges: [] },
    });

    expect(
      textOf(
        html.match(
          /<p\b[^>]*data-testid="group-pattern-intro"[^>]*>([\s\S]*?)<\/p>/,
        )[1],
      ),
    ).toBe(
      'Groups the selected ungrouped devices and switches whose names match.',
    );
  });

  test('offers the pattern used last', async () => {
    vi.stubGlobal(
      'localStorage',
      memoryStorage({ [GROUP_PATTERN_STORAGE_KEY]: '^(web|db)-' }),
    );

    const html = await render(GroupPatternDialog);

    expect(attribute(tagOf(html, 'group-pattern-field'), 'value')).toBe(
      '^(web|db)-',
    );
  });
});
