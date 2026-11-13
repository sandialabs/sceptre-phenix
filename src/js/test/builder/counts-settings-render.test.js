// The header's counts, resource totals and checks button, the Settings dialog
// and the Auto-group by name pattern dialog, rendered on the server. e2e
// presses them.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import BuilderChecks from '@/components/builder/BuilderChecks.vue';
import BuilderCounts from '@/components/builder/BuilderCounts.vue';
import BuilderResources from '@/components/builder/BuilderResources.vue';
import BuilderSettings from '@/components/builder/BuilderSettings.vue';
import GroupPatternDialog from '@/components/builder/dialogs/GroupPatternDialog.vue';
import { GROUP_PATTERN_STORAGE_KEY } from '@/builder/grouping.js';
import { setLayers, setNodeLayer } from '@/builder/model.js';
import { resetSettings, setSetting } from '@/builder/settings.js';

import { memoryStorage } from '../helpers/storage.js';
import { attribute, sampleDocument, tagOf, textOf } from './fixtures.js';
import { renderWithStore } from './render.js';

async function render(
  component,
  { doc = sampleDocument().doc, selection, props, readOnly = false } = {},
) {
  const { html } = await renderWithStore(component, {
    props,
    prepare(store) {
      store.doc = doc;
      store.readOnly = readOnly;
      if (selection) {
        store.selection = selection;
      }
    },
  });

  return html;
}

describe('the header’s checks button', () => {
  test('keeps its name, and the header’s tooltip describes it', async () => {
    const button = (html) =>
      html.match(
        /<button\b[^>]*data-testid="builder-checks"[\s\S]*?<\/button>/,
      )[0];
    const tipped = button(
      await render(BuilderChecks, { props: { tipFor: () => ({}) } }),
    );

    expect(attribute(tagOf(tipped, 'builder-checks'), 'aria-describedby')).toBe(
      'header-tip-checks',
    );
    expect(tipped).toMatch(
      /<span class="builder-visually-hidden"[^>]*>1 warning in this diagram<\/span>/,
    );

    // Without the header's tooltip, nothing describes it.
    const plain = button(await render(BuilderChecks));

    expect(
      attribute(tagOf(plain, 'builder-checks'), 'aria-describedby'),
    ).toBeNull();
  });
});

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

  test('end with the layers, only while the diagram defines layers', async () => {
    const { doc, alpha } = sampleDocument();
    const layered = setNodeLayer(
      setLayers(doc, [
        { id: 2, label: 'Control' },
        { id: 1, label: 'Field' },
      ]),
      alpha.id,
      1,
    );

    expect(await render(BuilderCounts, { doc })).not.toContain('count-layers');

    const html = await render(BuilderCounts, { doc: layered });
    const button = html.match(
      /<button\b[^>]*data-testid="count-layers"[^>]*>([\s\S]*?)<\/button>/,
    );

    expect(button).toBeTruthy();
    expect(textOf(button[1])).toBe('2 layers');
    expect(attribute(button[0], 'aria-describedby')).toBe('count-tip-layers');
    expect(textOf(html.match(/<ul\b[\s\S]*?<\/ul>/)[0])).toMatch(
      /, 0 notes, 2 layers$/,
    );
    expect(html).toMatch(
      /<span\b[^>]*id="count-tip-layers"[^>]*hidden[^>]*>\s*Select all nodes on a layer\s*<\/span>/,
    );
  });
});

describe('the header’s resource totals', () => {
  const totals = (html) => {
    const block = html.match(
      /<p\b[^>]*data-testid="builder-resources"[^>]*>([\s\S]*?)<\/p>/,
    );

    return block && textOf(block[1]);
  };

  test('are in words, with the defaults of phenix, apart from the counts', async () => {
    const html = await render(BuilderResources);

    expect(totals(html)).toBe('Devices take 2 vCPUs, 1 GB RAM');
    expect(await render(BuilderCounts)).not.toContain('builder-resources');
  });

  test('add the selection while it holds a device', async () => {
    const { doc, alpha, sw } = sampleDocument();

    expect(
      totals(
        await render(BuilderResources, {
          doc,
          selection: { nodes: [alpha.id], edges: [] },
        }),
      ),
    ).toBe('Devices take 2 vCPUs, 1 GB RAM. Selected: 1 vCPU, 512 MB RAM');
    expect(
      totals(
        await render(BuilderResources, {
          doc,
          selection: { nodes: [sw.id], edges: [] },
        }),
      ),
    ).toBe('Devices take 2 vCPUs, 1 GB RAM');
  });

  test('are not shown without a device to start', async () => {
    expect(
      await render(BuilderResources, {
        doc: { ...sampleDocument().doc, nodes: [] },
      }),
    ).not.toContain('builder-resources');
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

  test.each([
    [
      'Show node icons',
      'settings-node-icons',
      'settings-node-icons-hint',
      'showNodeIcons',
      true,
    ],
    [
      'Show node descriptions',
      'settings-node-descriptions',
      'settings-node-descriptions-hint',
      'showNodeDescriptions',
      true,
    ],
    [
      'Show connections',
      'settings-connections',
      'settings-connections-hint',
      'showConnections',
      true,
    ],
    [
      'Assign addresses on connect',
      'settings-assign-addresses',
      'settings-assign-addresses-hint',
      'assignAddressesOnConnect',
      true,
    ],
    [
      'Show coordinates',
      'settings-coordinates',
      'settings-coordinates-hint',
      'showCoordinates',
      false,
    ],
    [
      'Snap to grid',
      'settings-snap-to-grid',
      'settings-snap-hint',
      'snapToGrid',
      false,
    ],
  ])(
    'offers %s as a switch with its hint, and its default until changed',
    async (label, testId, hintId, key, on) => {
      const html = await render(BuilderSettings);
      const toggle = tagOf(html, testId);

      expect(attribute(toggle, 'role')).toBe('switch');
      expect(attribute(toggle, 'aria-checked')).toBe(String(on));
      expect(attribute(toggle, 'aria-describedby')).toBe(hintId);
      expect(textOf(html)).toContain(label);
      expect(html).toContain(`<p id="${hintId}"`);

      setSetting(key, !on, null);
      expect(
        attribute(tagOf(await render(BuilderSettings), testId), 'aria-checked'),
      ).toBe(String(!on));
    },
  );

  test('offers Node icon size: Off, Small, Medium or Large, Off until another is chosen', async () => {
    const html = await render(BuilderSettings);
    const [, group] = html.match(
      /<legend[^>]*>Node icon size<\/legend>([\s\S]*?)<\/fieldset>/,
    );
    const radios = [...group.matchAll(/<input\b[^>]*type="radio"[^>]*>/g)].map(
      ([tag]) => tag,
    );

    expect(radios.map((tag) => attribute(tag, 'value'))).toEqual([
      'off',
      'small',
      'medium',
      'large',
    ]);
    expect(radios.map((tag) => /\schecked\b/.test(tag))).toEqual([
      true,
      false,
      false,
      false,
    ]);
    expect(radios.map((tag) => attribute(tag, 'aria-describedby'))).toEqual(
      Array(4).fill('settings-icon-size-hint'),
    );
    expect(
      [...group.matchAll(/<label\b[^>]*>([\s\S]*?)<\/label>/g)].map(
        ([, inner]) => textOf(inner),
      ),
    ).toEqual(['Off', 'Small', 'Medium', 'Large']);
    expect(group).toContain('<p id="settings-icon-size-hint"');

    setSetting('nodeIconSize', 'medium', null);
    const medium = await render(BuilderSettings);

    expect(tagOf(medium, 'settings-icon-size-medium')).toMatch(/\schecked\b/);
    expect(tagOf(medium, 'settings-icon-size-off')).not.toMatch(/\schecked\b/);
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
      'settings-zoom-percent-hint',
    );
    expect(attribute(field, 'aria-invalid')).toBeNull();
    // Always enabled: typing a percentage chooses Custom.
    expect(field).not.toMatch(/\sdisabled\b/);

    expect(group).toContain('<p id="settings-zoom-percent-hint"');
    // The message is there, empty, from the start.
    expect(group).toMatch(
      /<p id="settings-zoom-percent-error"[^>]*role="alert"[^>]*>(<!--[\s\S]*?-->)*<\/p>/,
    );
  });

  // The layers have an editor of their own, not a section of Settings.
  // Edit layers…, at the bottom, opens it.
  test('opens the layers editor from Edit layers…, not from rows of its own', async () => {
    const html = await render(BuilderSettings, { props: { diagram: true } });

    expect(html).not.toContain('settings-layer-row');
    expect(html).not.toContain('data-testid="layer-row"');

    const button = tagOf(html, 'settings-edit-layers');

    expect(attribute(button, 'aria-haspopup')).toBe('dialog');
    expect(attribute(button, 'aria-describedby')).toBe('settings-layers-hint');

    // On the drafts page it opens the Layers tab, not a dialog.
    const drafts = await render(BuilderSettings, { props: { diagram: false } });

    expect(
      attribute(tagOf(drafts, 'settings-edit-layers'), 'aria-haspopup'),
    ).toBeNull();
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

  test('asks for the pattern in a named field', async () => {
    const html = await render(GroupPatternDialog);
    const field = tagOf(html, 'group-pattern-field');

    expect(html).toMatch(
      /<h2 id="group-pattern-dialog-title"[^>]*>Auto-group by name pattern<\/h2>/,
    );
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
    expect(html).toContain('<p id="group-pattern-hint"');
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
