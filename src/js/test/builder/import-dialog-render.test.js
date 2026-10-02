// The options Import offers for a topology: what to do with the topologies
// it includes, and whether the diagram is a copy. The rules are tested on
// their own (importOptions.js), and the dialog is rendered on the server
// for what it shows as it opens: the form a link from the Configs page
// presets, and that an option shows only when it applies. Choosing a file,
// focus, the refusals and the draft that results need a browser, so they
// are left to builder-io.spec.js and builder-integration.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h, reactive } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import ImportDialog from '@/components/builder/dialogs/ImportDialog.vue';
import {
  importIntro,
  includesHint,
  newNameProblem,
  notAvailable,
  proposedName,
  useImportOptions,
} from '@/components/builder/dialogs/importOptions.js';
import { CONFIG_NAME_RULE } from '@/builder/publish.js';
import { useBuilderStore } from '@/builder/store.js';

const TOPOLOGIES = [
  { name: 'plain' },
  { name: 'site', includeCount: 1 },
  { name: 'water', includeCount: 3 },
  { name: 'old', builder: 'builder-xml' },
  { name: 'old-site', builder: 'builder-xml', includeCount: 2 },
  { name: 'site-copy' },
  { name: 'site-combined' },
  { name: 'site-combined-2' },
  'named',
];

const SOURCES = {
  images: [],
  scenarios: [],
  experiments: [{ name: 'site' }],
  topologies: TOPOLOGIES,
};

const FILE = [
  'kind: Topology',
  'metadata:',
  '  name: Plant Floor',
  'spec:',
  '  includeTopologies: [corp-services, lab-dns]',
  '  nodes: []',
].join('\n');

// The options for a form, as the dialog holds them.
function optionsFor(fields = {}) {
  const form = reactive({
    source: 'stored',
    kind: 'topology',
    name: '',
    content: '',
    fileName: '',
    ...fields,
  });

  return { form, options: useImportOptions(form, () => TOPOLOGIES) };
}

async function render(props = {}, sources = SOURCES) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(ImportDialog, props) });

  app.use(pinia);
  useBuilderStore(pinia).sources = sources;

  return renderToString(app);
}

// The attributes of the one element the pattern finds.
function tag(html, pattern) {
  const found = html.match(new RegExp(`<[a-z]+\\b[^>]*${pattern}[^>]*>`));

  expect(found, pattern).not.toBeNull();

  return found[0];
}

// The text of the element with that id, without Vue's comment markers.
function textOf(html, id) {
  const found = html.match(new RegExp(`id="${id}"[^>]*>([\\s\\S]*?)</`));

  expect(found, id).not.toBeNull();

  return found[1]
    .replace(/<!--[^>]*-->/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

describe('the "Included topologies" choice', () => {
  test('is offered only for a topology that includes some', () => {
    expect(optionsFor({ name: 'site' }).options.showIncludes).toBe(true);
    expect(optionsFor({ name: 'old-site' }).options.showIncludes).toBe(true);

    for (const fields of [
      {},
      { name: 'plain' },
      { name: 'named' },
      { name: 'old' },
      // An experiment of the same name holds its nodes merged already.
      { kind: 'experiment', name: 'site' },
      { name: 'not-listed' },
    ]) {
      expect(optionsFor(fields).options.showIncludes, fields.name).toBe(false);
    }
  });

  test('says how many topologies are included, by the name of a stored one', () => {
    expect(optionsFor({ name: 'site' }).options.includesText).toBe(
      'site includes 1 other topology.',
    );
    expect(optionsFor({ name: 'water' }).options.includesText).toBe(
      'water includes 3 other topologies.',
    );
    expect(includesHint(2)).toBe('This topology includes 2 other topologies.');
  });

  test('is offered for a config file that includes topologies, and starts on keep', () => {
    const { options } = optionsFor({ source: 'file', content: FILE });

    expect(options.showIncludes).toBe(true);
    expect(options.includeCount).toBe(2);
    expect(options.includesText).toBe(
      'This topology includes 2 other topologies.',
    );
    expect(options.includes).toBe('keep');
    expect(options.mode).toBe('import');

    for (const content of [
      '',
      'kind: Topology\nmetadata: {name: lone}\nspec: {nodes: []}',
      'kind: Experiment\nspec: {includeTopologies: [a]}',
      'not: [closed',
    ]) {
      expect(
        optionsFor({ source: 'file', content }).options.showIncludes,
        content,
      ).toBe(false);
    }
  });

  test('a stored name chosen earlier says nothing about a file', () => {
    const { form, options } = optionsFor({ name: 'site' });

    form.source = 'file';
    expect(options.showIncludes).toBe(false);
    expect(options.showCopy).toBe(false);
  });
});

describe('"Create a new topology as a copy"', () => {
  test('is offered for a stored topology only', () => {
    expect(optionsFor({ name: 'plain' }).options.showCopy).toBe(true);
    expect(optionsFor({ name: 'site' }).options.showCopy).toBe(true);
    expect(optionsFor({ name: 'old' }).options.showCopy).toBe(true);
    expect(optionsFor({ name: 'plain' }).options.copyOf).toBe('plain');

    expect(optionsFor().options.showCopy).toBe(false);
    expect(
      optionsFor({ kind: 'experiment', name: 'site' }).options.showCopy,
    ).toBe(false);
    // A draft of a config file is never linked to a stored topology.
    expect(optionsFor({ source: 'file', content: FILE }).options.showCopy).toBe(
      false,
    );
  });

  test('is hidden while Combine is chosen, which makes a new topology already', () => {
    const { options } = optionsFor({ name: 'site' });

    options.copy = true;
    expect(options.mode).toBe('copy');

    options.includes = 'combine';
    expect(options.showCopy).toBe(false);
    expect(options.mode).toBe('combine');

    // Back on keep, the box is as it was left.
    options.includes = 'keep';
    expect(options.showCopy).toBe(true);
    expect(options.copy).toBe(true);
    expect(options.mode).toBe('copy');
  });
});

describe('"New topology name"', () => {
  test('is asked for only with a copy or a combined topology', () => {
    const { options } = optionsFor({ name: 'site' });

    expect(options.showNewName).toBe(false);
    expect(options.newName).toBe('');

    options.copy = true;
    expect(options.showNewName).toBe(true);

    options.copy = false;
    options.includes = 'combine';
    expect(options.showNewName).toBe(true);

    options.includes = 'keep';
    expect(options.showNewName).toBe(false);
  });

  test('is proposed from the topology and the choice, with a name no stored topology has', () => {
    const { options } = optionsFor({ name: 'site' });

    // site-copy exists, and so do site-combined and site-combined-2.
    options.copy = true;
    expect(options.newName).toBe('site-copy-2');

    options.includes = 'combine';
    expect(options.newName).toBe('site-combined-3');

    const water = optionsFor({ name: 'water' }).options;

    water.copy = true;
    expect(water.newName).toBe('water-copy');

    expect(proposedName('site', 'copy', [])).toBe('site-copy');
    expect(proposedName('site', 'combine', ['site-combined'])).toBe(
      'site-combined-2',
    );
  });

  test("a config file's proposal is its name, as a config name", () => {
    const { options } = optionsFor({ source: 'file', content: FILE });

    options.includes = 'combine';
    expect(options.newName).toBe('Plant-Floor-combined');

    // A file without a usable name still gets a proposal.
    expect(proposedName('', 'combine', [])).toBe('topology-combined');
    expect(proposedName('???', 'copy', [])).toBe('topology-copy');
  });

  test('follows the choice until the user types in it', () => {
    const { options } = optionsFor({ name: 'water' });

    options.copy = true;
    expect(options.newName).toBe('water-copy');
    options.includes = 'combine';
    expect(options.newName).toBe('water-combined');

    options.typeName('river');
    expect(options.newName).toBe('river');

    options.includes = 'keep';
    expect(options.newName).toBe('river');

    // Emptied, it stays empty rather than fill itself in again.
    options.typeName('');
    expect(options.newName).toBe('');
  });

  test('another source starts from the defaults, and from its own proposal', () => {
    const { form, options } = optionsFor({ name: 'water' });

    options.includes = 'combine';
    options.typeName('river');

    form.name = 'site';
    expect(options.includes).toBe('keep');
    expect(options.copy).toBe(false);
    expect(options.showNewName).toBe(false);

    options.copy = true;
    expect(options.newName).toBe('site-copy-2');

    form.kind = 'experiment';
    expect(options.copy).toBe(false);

    form.source = 'file';
    form.content = FILE;
    options.includes = 'combine';
    options.typeName('floor');
    // Another file is another source.
    form.content = `${FILE}\n`;
    expect(options.includes).toBe('keep');
    options.includes = 'combine';
    expect(options.newName).toBe('Plant-Floor-combined');
  });

  test('is checked before the server is asked, with a message for each mistake', () => {
    const taken = ['site', 'site-copy'];

    expect(newNameProblem('', taken)).toBe('Enter a name for the topology.');
    expect(newNameProblem('my site', taken)).toBe(
      `The topology name "my site" is not allowed. ${CONFIG_NAME_RULE} For example: my-site`,
    );
    expect(newNameProblem('a'.repeat(513), taken)).toBe(
      'Enter a name of at most 512 bytes.',
    );
    expect(newNameProblem('a'.repeat(512), taken)).toBe('');
    expect(newNameProblem('site-copy', taken)).toBe(
      'A topology named site-copy already exists. Enter another name.',
    );
    // The imported topology's own name is one of the stored names.
    expect(newNameProblem('site', taken)).toBe(
      'A topology named site already exists. Enter another name.',
    );
    expect(newNameProblem('site-copy-2', taken)).toBe('');
  });

  test('the form reports the problem of the name it holds, trimmed, and none without one to ask for', () => {
    const { options } = optionsFor({ name: 'site' });

    options.typeName('site-copy');
    // No new name is asked for, so what the field held does not matter.
    expect(options.problem()).toBe('');

    options.copy = true;
    expect(options.problem()).toBe(
      'A topology named site-copy already exists. Enter another name.',
    );

    options.typeName('  ');
    expect(options.problem()).toBe('Enter a name for the topology.');

    options.typeName(' site-b ');
    expect(options.problem()).toBe('');
  });
});

describe('the request', () => {
  test('a plain import names its source and nothing else', () => {
    expect(optionsFor({ name: 'plain' }).options.request()).toEqual({
      kind: 'topology',
      name: 'plain',
    });
    expect(
      optionsFor({ kind: 'experiment', name: 'site' }).options.request(),
    ).toEqual({ kind: 'experiment', name: 'site' });
    expect(
      optionsFor({
        source: 'file',
        content: 'kind: Topology',
      }).options.request(),
    ).toEqual({ content: 'kind: Topology' });
  });

  test('the choice about includes is sent when it was offered', () => {
    const { options } = optionsFor({ name: 'site' });

    expect(options.request()).toEqual({
      kind: 'topology',
      name: 'site',
      includes: 'keep',
    });

    options.includes = 'combine';
    expect(options.request()).toEqual({
      kind: 'topology',
      name: 'site',
      includes: 'combine',
      newName: 'site-combined-3',
    });
  });

  test('a copy sends the new name, trimmed', () => {
    const { options } = optionsFor({ name: 'plain' });

    options.copy = true;
    options.typeName(' plain-two ');
    expect(options.request()).toEqual({
      kind: 'topology',
      name: 'plain',
      copy: true,
      newName: 'plain-two',
    });

    // A copy that keeps its included nodes read only.
    const site = optionsFor({ name: 'site' }).options;

    site.copy = true;
    expect(site.request()).toEqual({
      kind: 'topology',
      name: 'site',
      includes: 'keep',
      copy: true,
      newName: 'site-copy-2',
    });
  });

  test('Combine is not also a copy, even with the box left checked', () => {
    const { options } = optionsFor({ name: 'site' });

    options.copy = true;
    options.includes = 'combine';
    expect(options.request()).toEqual({
      kind: 'topology',
      name: 'site',
      includes: 'combine',
      newName: 'site-combined-3',
    });
  });

  test('a config file is combined under its own proposal', () => {
    const { options } = optionsFor({ source: 'file', content: FILE });

    options.includes = 'combine';
    expect(options.request()).toEqual({
      content: FILE,
      includes: 'combine',
      newName: 'Plant-Floor-combined',
    });
  });
});

describe('the dialog as it opens', () => {
  test('shows no option before a source is chosen', async () => {
    const html = await render();

    for (const id of [
      'import-includes',
      'import-combine',
      'import-copy',
      'import-new-name',
      'import-intro',
    ]) {
      expect(html, id).not.toContain(id);
    }
    expect(tag(html, 'data-testid="builder-dialog"')).not.toContain(
      'aria-describedby',
    );
  });

  test('opened from Configs on a topology with includes, it shows the choice, on keep, and the copy box', async () => {
    const html = await render({ initial: { kind: 'topology', name: 'water' } });

    // The form is preset to the topology.
    expect(tag(html, 'value="stored"')).toContain('checked');
    expect(tag(html, 'value="water"')).toContain('selected');

    const group = tag(html, 'data-testid="import-includes"');

    expect(group).toMatch(/^<fieldset/);
    expect(html).toMatch(/<legend[^>]*>Included topologies<\/legend>/);
    expect(textOf(html, 'import-includes-hint')).toBe(
      'water includes 3 other topologies.',
    );

    const keep = tag(html, 'data-testid="import-keep"');
    const combine = tag(html, 'data-testid="import-combine"');

    for (const radio of [keep, combine]) {
      expect(radio).toContain('type="radio"');
      expect(radio).toContain('name="import-includes"');
    }
    expect(keep).toContain('value="keep"');
    expect(keep).toContain('checked');
    expect(keep).toContain(
      'aria-describedby="import-includes-hint import-keep-hint"',
    );
    expect(combine).toContain('value="combine"');
    expect(combine).not.toContain('checked');
    expect(combine).toContain(
      'aria-describedby="import-includes-hint import-combine-hint"',
    );
    expect(html).toMatch(
      /data-testid="import-keep"[^>]*>\s*Keep included nodes read only\s*</,
    );
    expect(html).toMatch(
      /data-testid="import-combine"[^>]*>\s*Combine into one new topology\s*</,
    );
    expect(textOf(html, 'import-keep-hint')).toBe(
      'Their nodes are shown but cannot be changed here. Publishing keeps the includes.',
    );
    expect(textOf(html, 'import-combine-hint')).toBe(
      'Their nodes are copied into the diagram and can be changed. Publishing creates a new topology.',
    );

    const copy = tag(html, 'data-testid="import-copy"');

    expect(copy).toContain('type="checkbox"');
    expect(copy).not.toContain('checked');
    expect(copy).toContain('aria-describedby="import-copy-hint"');
    expect(html).toMatch(
      /data-testid="import-copy"[^>]*>\s*Create a new topology as a copy\s*</,
    );
    expect(textOf(html, 'import-copy-hint')).toBe(
      'The draft is not linked to water. Publishing creates a new topology and leaves water as it is.',
    );

    // No name is asked for until one of the two is chosen.
    expect(html).not.toContain('import-new-name');
    // Each control comes after the one that reveals it.
    expect(html.indexOf('id="import-name"')).toBeLessThan(
      html.indexOf('data-testid="import-includes"'),
    );
    expect(html.indexOf('data-testid="import-includes"')).toBeLessThan(
      html.indexOf('data-testid="import-copy"'),
    );
    expect(html.indexOf('data-testid="import-copy"')).toBeLessThan(
      html.indexOf('id="import-error"'),
    );
  });

  test('says why it opened, as the description of the dialog', async () => {
    const html = await render({ initial: { kind: 'topology', name: 'water' } });

    expect(tag(html, 'data-testid="builder-dialog"')).toContain(
      'aria-describedby="import-intro"',
    );
    expect(textOf(html, 'import-intro')).toBe(
      'Topology water has no Builder diagram to open. Import it to make one.',
    );
    expect(html.indexOf('id="import-intro"')).toBeLessThan(
      html.indexOf('<form'),
    );
    expect(importIntro('water', false)).toBe(textOf(html, 'import-intro'));
  });

  test('a topology with a legacy diagram is introduced as one to convert', async () => {
    const html = await render({ initial: { kind: 'topology', name: 'old' } });

    expect(textOf(html, 'import-intro')).toBe(
      'Topology old has a legacy Builder diagram. Import it to convert the diagram.',
    );
    expect(tag(html, 'value="old"')).toContain('selected');
    expect(textOf(html, 'import-legacy-hint')).toBe(
      'This topology has a legacy Builder diagram. Its layout is converted. Publishing the draft to this topology replaces the legacy diagram.',
    );
    // It has no includes: the copy box is the only option.
    expect(html).not.toContain('import-includes');
    expect(html).toContain('data-testid="import-copy"');
  });

  test('a topology without includes is offered the copy only', async () => {
    const html = await render({ initial: { kind: 'topology', name: 'plain' } });

    expect(html).not.toContain('import-includes');
    expect(html).not.toContain('import-combine');
    expect(html).toContain('data-testid="import-copy"');
    expect(textOf(html, 'import-copy-hint')).toBe(
      'The draft is not linked to plain. Publishing creates a new topology and leaves plain as it is.',
    );
  });

  test('the sentence for a topology that is no longer offered names it', () => {
    expect(notAvailable('water')).toBe(
      'Topology water is not available to import: it does not exist, or you may not read it.',
    );
  });
});
