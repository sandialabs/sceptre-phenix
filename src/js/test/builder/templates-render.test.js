// The palette's device templates and the template editor, rendered on the
// server, and the commands that go with them. What needs a browser (the
// menu, focus, saving) is left to builder-templates.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import BuilderPalette from '@/components/builder/BuilderPalette.vue';
import TemplateDialog from '@/components/builder/dialogs/TemplateDialog.vue';
import {
  READ_ONLY,
  VIEW_API,
  availability,
  createCommandContext,
  getCommand,
  runCommand,
} from '@/builder/commands.js';
import { addTemplate, createDocument } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import { blankTemplate } from '@/builder/templates.js';
import { MAX_TEMPLATES } from '@/builder/validate.js';

import { sampleDocument, tags, withTemplates } from './fixtures.js';
import { ICON_DATA, ICON_KEY } from './png.js';

const PLC = {
  name: 'PLC',
  description: 'A controller',
  device: {
    iconKey: 'firewall',
    spec: {
      type: 'VirtualMachine',
      general: { hostname: 'plc', description: '', vm_type: 'kvm' },
      hardware: { os_type: 'linux', drives: [{ image: 'plc.qc2' }] },
      network: { interfaces: [] },
    },
  },
};

// What an element holds, as text: tags out, white space as one space.
function textOf(html) {
  return html
    .replace(/<[^>]*>/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

// A component rendered with a Builder store that `prepare` sets up first.
async function render(component, props, prepare = () => {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });

  app.use(pinia);

  const store = useBuilderStore(pinia);

  store.doc = createDocument({ name: 'Plant' });
  prepare(store);

  return { html: await renderToString(app), store };
}

// A diagram of `count` templates named T0, T1, ...
function withTemplateCount(count) {
  let doc = createDocument();

  for (let index = 0; index < count; index += 1) {
    doc = addTemplate(doc, { ...PLC, name: `T${index}` }).doc;
  }

  return doc;
}

// Why "+" does nothing, as its description says it: a hidden element, and
// its text.
function whyOf(html) {
  const [, attributes, inner] = html.match(
    /<p([^>]*id="palette-new-template-why"[^>]*)>([\s\S]*?)<\/p>/,
  );

  return [/\shidden/.test(attributes) ? 'hidden' : 'shown', textOf(inner)];
}

describe('the palette’s device templates', () => {
  // The five built-in ones alone look as the palette always did.
  test('with one group, the list has no heading of its own', async () => {
    const { html } = await render(BuilderPalette);

    expect(tags(html, 'h4')).toEqual([]);
    expect(html).toContain('>Device templates</h3>');

    const [list] = html.match(
      /<ul[^>]*data-testid="palette-templates-builtin"[\s\S]*?<\/ul>/,
    );

    expect(list).toContain('aria-label="Built-in"');
    expect(
      [...list.matchAll(/data-testid="(palette-template-[a-z]+)"/g)].map(
        (match) => match[1],
      ),
    ).toEqual([
      'palette-template-server',
      'palette-template-workstation',
      'palette-template-router',
      'palette-template-firewall',
      'palette-template-external',
    ]);
    expect(list).toContain('aria-label="Add Router device"');
    expect(list).toContain('aria-label="Add External device"');
    expect(list).not.toContain('aria-haspopup');
    expect(html).not.toContain('palette-templates-diagram');
  });

  test('the diagram’s templates come first, each with a menu, and both groups are named', async () => {
    let added;
    const { html } = await render(BuilderPalette, {}, (store) => {
      added = addTemplate(store.doc, {
        ...PLC,
        device: { ...PLC.device, icon: ICON_KEY },
      });
      store.doc = {
        ...added.doc,
        icons: { [ICON_KEY]: { name: 'plc', data: ICON_DATA } },
      };
    });
    const id = added.template.id;

    expect(
      [...html.matchAll(/<h4[^>]*>([\s\S]*?)<\/h4>/g)].map(([, inner]) =>
        textOf(inner),
      ),
    ).toEqual(['This diagram', 'Built-in']);
    expect(html.indexOf('palette-templates-diagram')).toBeLessThan(
      html.indexOf('palette-templates-builtin'),
    );

    const [list] = html.match(
      /<ul[^>]*data-testid="palette-templates-diagram"[\s\S]*?<\/ul>/,
    );

    expect(list).toContain('aria-label="This diagram"');
    expect(list).toContain(`data-testid="palette-template-diagram-${id}"`);
    expect(list).toContain('aria-label="Add PLC device"');
    // Its description is the entry's, by a hidden element.
    expect(list).toContain(
      `aria-describedby="palette-template-diagram-${id}-hint"`,
    );
    expect(
      [...list.matchAll(/<span[^>]*>([\s\S]*?)<\/span>/g)].map(([, inner]) =>
        textOf(inner),
      ),
    ).toEqual(['A controller', 'PLC']);
    // Its custom icon is drawn, as an image of the diagram's own copy.
    expect(list).toContain(`src="data:image/png;base64,${ICON_DATA}"`);

    const [menu] = tags(list, 'button').filter((tag) =>
      tag.includes('aria-haspopup="menu"'),
    );

    expect(menu).toContain(`data-testid="palette-template-actions-${id}"`);
    expect(menu).toContain('aria-label="Actions for template PLC"');
    expect(menu).toContain('aria-expanded="false"');
    expect(menu).not.toContain('aria-disabled');
  });

  test('an entry without a description has none to point at', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.doc = addTemplate(store.doc, { ...PLC, description: '' }).doc;
    });
    const [entry] = tags(html, 'button').filter((tag) =>
      tag.includes('palette-template-diagram-'),
    );

    expect(entry).not.toContain('aria-describedby');
  });

  test('"+" opens the template editor, named for what it does', async () => {
    const { html } = await render(BuilderPalette);
    const [button] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="palette-new-template"'),
    );

    expect(button).toContain('aria-label="New device template"');
    expect(button).not.toContain('aria-disabled');
    expect(button).not.toContain(' disabled');
    expect(button).not.toContain('aria-describedby');
  });

  test('a read-only draft keeps "+" in reach and says why it does nothing', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.doc = addTemplate(store.doc, PLC).doc;
      store.readOnly = true;
    });
    const buttons = tags(html, 'button');
    const [plus] = buttons.filter((tag) =>
      tag.includes('data-testid="palette-new-template"'),
    );
    const [menu] = buttons.filter((tag) =>
      tag.includes('aria-haspopup="menu"'),
    );
    const entries = buttons.filter((tag) =>
      tag.includes('class="builder-palette__item"'),
    );

    expect(plus).toContain('aria-disabled="true"');
    expect(plus).not.toMatch(/\sdisabled(\s|>|=)/);
    expect(plus).toContain('aria-describedby="palette-new-template-why"');
    expect(whyOf(html)).toEqual(['hidden', 'This diagram is read only.']);
    expect(menu).toContain('aria-disabled="true"');
    // Device, Switch, Note, Group, the four drawings, the diagram's
    // template and the five built-in ones.
    expect(entries).toHaveLength(14);
    expect(entries.every((tag) => /\sdisabled(\s|>|=)/.test(tag))).toBe(true);
  });

  test('a diagram with 50 templates says so on "+"', async () => {
    const { html } = await render(BuilderPalette, {}, (store) => {
      store.doc = withTemplateCount(MAX_TEMPLATES);
    });
    const [plus] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="palette-new-template"'),
    );

    expect(plus).toContain('aria-disabled="true"');
    expect(whyOf(html)).toEqual([
      'hidden',
      'This diagram has 50 templates, the most it can hold.',
    ]);
  });
});

describe('the template editor', () => {
  const blank = () => ({ mode: 'diagram-new', template: blankTemplate() });

  test('a new template: its title, its two fields, the node’s form and Save to diagram', async () => {
    const { html } = await render(TemplateDialog, blank());
    const [dialog] = tags(html, 'dialog');
    const [title] = html.match(/<h2[^>]*>[\s\S]*?<\/h2>/);

    expect(dialog).toContain('builder-template-editor');
    expect(dialog).toContain('aria-labelledby="template-dialog-title"');
    expect(textOf(title)).toBe('New device template');
    expect(
      [...html.matchAll(/<h3[^>]*>([\s\S]*?)<\/h3>/g)].map(([, inner]) =>
        textOf(inner),
      ),
    ).toEqual(['Template', 'Node fields']);

    const [name] = tags(html, 'input').filter((tag) =>
      tag.includes('id="template-name"'),
    );
    const [description] = tags(html, 'input').filter((tag) =>
      tag.includes('id="template-description"'),
    );

    expect(name).toContain('required');
    expect(name).not.toContain('aria-invalid');
    expect(description).toContain(
      'aria-describedby="template-description-hint"',
    );
    expect(html).toContain(
      'Shown as a tooltip in Add nodes. It is not written to the node.',
    );

    // The Inspector itself, in its template variant, named by the heading.
    const [section] = tags(html, 'section');

    expect(section).toContain('builder-inspector--template');
    expect(section).toContain('aria-labelledby="template-fields-title"');
    expect(html).toContain('id="template-fields-title"');
    expect(html).toContain('Hostname');
    expect(html).toContain('Fill Color');
    expect(html).not.toContain('data-testid="inspector-actions"');
    expect(html).not.toContain('Connection points');

    const [save] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="template-save"'),
    );
    const [, saveText] = html.match(
      /data-testid="template-save"[^>]*>([\s\S]*?)<\/button>/,
    );

    expect(save).toContain('type="submit"');
    expect(save).toContain('form="template-about"');
    expect(textOf(saveText)).toBe('Save to diagram');
    expect(html).toContain('data-testid="template-cancel"');
  });

  test('a template of the diagram: named in the title, with Save', async () => {
    const { doc, template } = addTemplate(createDocument(), PLC);
    const { html } = await render(
      TemplateDialog,
      { mode: 'diagram-edit', template },
      (store) => {
        store.doc = doc;
      },
    );
    const [title] = html.match(/<h2[^>]*>[\s\S]*?<\/h2>/);
    const [, saveText] = html.match(
      /data-testid="template-save"[^>]*>([\s\S]*?)<\/button>/,
    );
    const [name] = tags(html, 'input').filter((tag) =>
      tag.includes('id="template-name"'),
    );
    const [description] = tags(html, 'input').filter((tag) =>
      tag.includes('id="template-description"'),
    );

    expect(textOf(title)).toBe('Edit template PLC');
    expect(textOf(saveText)).toBe('Save');
    expect(name).toContain('value="PLC"');
    expect(description).toContain('value="A controller"');
    // Its device's values are the form's.
    expect(html).toContain('value="plc.qc2"');
    // It is the only template of that name.
    expect(html).not.toContain('Another template has this name.');
  });

  test('a name another template of the diagram has is said, not refused', async () => {
    const first = addTemplate(createDocument(), PLC);
    const second = addTemplate(first.doc, { ...PLC, name: '  plc ' });
    const { html } = await render(
      TemplateDialog,
      { mode: 'diagram-edit', template: first.template },
      (store) => {
        store.doc = second.doc;
      },
    );
    const [hint] = html.match(/<p id="template-name-hint"[^>]*>[\s\S]*?<\/p>/);

    expect(hint).toContain('role="status"');
    expect(textOf(hint)).toBe('Another template has this name.');
  });

  test('the question it asks before dropping changes is there, not shown', async () => {
    const { html } = await render(TemplateDialog, blank());
    const [group] = tags(html, 'div').filter((tag) =>
      tag.includes('data-testid="template-discard"'),
    );

    expect(group).toContain('display:none');
    expect(group).toContain('aria-labelledby="template-discard-question"');
    expect(html).toContain('Discard changes to this template?');
    expect(html).toContain('data-testid="template-keep"');
    expect(html).toContain('data-testid="template-discard-confirm"');
  });

  test('a template with a custom icon shows it, from the icons it is given', async () => {
    const template = {
      ...PLC,
      device: { ...PLC.device, icon: ICON_KEY },
    };
    const { html } = await render(TemplateDialog, {
      mode: 'diagram-new',
      template,
      icons: { [ICON_KEY]: { name: 'plc icon', data: ICON_DATA } },
    });

    expect(html).toContain(`src="data:image/png;base64,${ICON_DATA}"`);
    expect(html).toContain('plc icon');
  });
});

describe('the template commands', () => {
  function context({ store = {}, view = {} } = {}) {
    const fullView = { editing: true, dialog: '', ...view };

    for (const name of VIEW_API) {
      fullView[name] ??= vi.fn();
    }

    return createCommandContext({
      store: withTemplates({
        doc: sampleDocument().doc,
        readOnly: false,
        selection: { nodes: [], edges: [] },
        announce: vi.fn(),
        addNode: vi.fn(() => ({ id: 'added' })),
        ...store,
      }),
      view: fullView,
    });
  }

  test('New device template opens the editor on a new template of the diagram', () => {
    const ctx = context();
    const command = getCommand('templates.new');

    expect(command).toMatchObject({
      title: 'New device template',
      group: 'Add',
    });
    expect(command.keys).toBeUndefined();
    expect(command).not.toHaveProperty('phrase');
    expect(availability(command, ctx)).toBe(true);
    expect(command.detail(ctx)).toBe('Saved in this diagram');

    expect(runCommand('templates.new', ctx)).toBe(true);
    expect(ctx.view.openDialog).toHaveBeenCalledWith('template', {
      mode: 'diagram-new',
    });
  });

  test('it names the selected device it starts from', () => {
    const { doc, alpha } = sampleDocument();
    const ctx = context({ store: { doc, selectedNode: alpha } });

    expect(getCommand('templates.new').detail(ctx)).toBe(
      'Saved in this diagram, from alpha',
    );
  });

  test('it says why it cannot run: a read-only draft, a full diagram', () => {
    const readOnly = context({ store: { readOnly: true } });
    const full = context({ store: { doc: withTemplateCount(MAX_TEMPLATES) } });
    const command = getCommand('templates.new');

    expect(availability(command, readOnly)).toBe(READ_ONLY);
    expect(availability(command, full)).toBe(
      'This diagram has 50 templates, the most it can hold.',
    );
    expect(runCommand(command, full)).toBe(false);
    expect(full.view.openDialog).not.toHaveBeenCalled();
    expect(full.store.announce).toHaveBeenCalledWith(
      'This diagram has 50 templates, the most it can hold.',
    );
  });

  test('Add device offers the plain device, then every template of the palette by group', () => {
    const { doc, template } = addTemplate(sampleDocument().doc, PLC);
    const ctx = context({ store: { doc } });
    const choices = getCommand('add.device').choices(ctx);

    expect(choices.map((choice) => [choice.title, choice.detail])).toEqual([
      ['Device', 'A virtual machine, container or external device'],
      ['PLC', 'This diagram · plc.qc2'],
      ['Server', 'Built-in · ubuntu.qc2'],
      ['Workstation', 'Built-in · windows10.qc2'],
      ['Router', 'Built-in · minirouter.qc2'],
      ['Firewall', 'Built-in · vyos.qc2'],
      ['External device', 'Built-in · no image'],
    ]);
    expect(choices[1]).toMatchObject({
      id: `diagram:${template.id}`,
      value: `diagram:${template.id}`,
      icon: 'firewall',
      keywords: ['A controller'],
    });

    // The choice adds the device of its template.
    runCommand('add.device', ctx, choices[1]);
    expect(ctx.store.addNode).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'device',
        hostname: 'plc',
        look: { iconKey: 'firewall' },
      }),
    );

    runCommand('add.device', ctx, choices[0]);
    expect(ctx.store.addNode.mock.calls[1][0]).toMatchObject({
      kind: 'device',
    });
    expect(ctx.store.addNode.mock.calls[1][0]).not.toHaveProperty('spec');
  });
});
