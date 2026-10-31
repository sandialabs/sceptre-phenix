// The Node Editor: its host (nodeEditorHost in nodeEditor.js), and its dialog
// rendered on the server (dialogs/NodeEditorDialog.vue).

import { describe, expect, test } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia, setActivePinia } from 'pinia';

import NodeEditorDialog from '@/components/builder/dialogs/NodeEditorDialog.vue';
import { applyFormData, inspectorTarget } from '@/builder/adapters/forms.js';
import { addNode, findNode } from '@/builder/model.js';
import { nodeEditorHost } from '@/builder/nodeEditor.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument, tags, textOf } from './fixtures.js';

// A Builder store on the sample diagram.
function sampleStore() {
  const pinia = createPinia();

  setActivePinia(pinia);

  const store = useBuilderStore(pinia);
  const sample = sampleDocument();

  // The draft as loaded, so an undo goes back to it.
  store.commit(sample.doc, '');

  return { pinia, store, ...sample };
}

// Renders the dialog for the node nodeId picks. prepare changes the store
// first, and what it returns joins the sample's nodes.
async function renderDialog(nodeId, prepare = () => ({})) {
  const { pinia, store, ...sample } = sampleStore();
  const nodes = { ...sample, ...prepare(store, sample) };

  const app = createSSRApp({
    render: () => h(NodeEditorDialog, { nodeId: nodeId(nodes) }),
  });

  app.use(pinia);

  return { html: await renderToString(app), store };
}

describe('the Node Editor’s host', () => {
  test('reads the store, and keeps the node it opened for', () => {
    const { store, alpha, bravo } = sampleStore();
    const said = [];
    const host = nodeEditorHost(store, alpha.id, {
      announce: (message) => said.push(message),
    });

    expect(host.doc).toBe(store.doc);
    expect(host.schema).toBe(store.schema);
    expect(host.inspectorSelection).toEqual({ type: 'node', id: alpha.id });

    store.select({ nodes: [bravo.id], edges: [] });
    store.readOnly = true;

    expect(host.inspectorSelection).toEqual({ type: 'node', id: alpha.id });
    expect(host.readOnly).toBe(true);

    host.announce('Unapplied changes.');
    expect(said).toEqual(['Unapplied changes.']);
  });

  test('a commit is one undo step of the store', () => {
    const { store, alpha } = sampleStore();
    const host = nodeEditorHost(store, alpha.id, { announce: () => {} });
    const selection = host.inspectorSelection;
    const data = inspectorTarget(store.doc, selection).data;
    const next = applyFormData(store.doc, selection, {
      ...data,
      hostname: 'alpha-2',
    });

    expect(host.commit(next, 'Applied changes to Device alpha')).toBeTruthy();
    expect(host.doc).toBe(store.doc);
    expect(findNode(store.doc, alpha.id).device.hostname).toBe('alpha-2');
    expect(store.history.undoLabel()).toBe('Applied changes to Device alpha');
    expect(store.announcement).toMatch(/^Applied changes to Device alpha/);

    store.undo();

    expect(findNode(store.doc, alpha.id).device.hostname).toBe('alpha');
  });

  test('the actions that act at once are the store’s', () => {
    const { store, alpha } = sampleStore();
    const host = nodeEditorHost(store, alpha.id, { announce: () => {} });
    const before = findNode(store.doc, alpha.id).device.interfaces?.length;

    host.moveNodes([{ id: alpha.id, position: { x: 40, y: 80 } }]);
    host.setNodeLayer(alpha.id, 2);

    const node = findNode(store.doc, alpha.id);

    expect(node.position).toEqual({ x: 40, y: 80 });
    expect(node.device.layer).toBe(2);
    expect(node.device.interfaces?.length).toBe(before);
    expect(store.history.undoLabel()).toBe(
      'Changed the layer of Device alpha to Layer 2',
    );
  });
});

describe('the Node Editor dialog', () => {
  test('is named for the node, and holds its form, Cancel and Apply', async () => {
    const { html } = await renderDialog(({ alpha }) => alpha.id);
    const [dialog] = tags(html, 'dialog');

    expect(dialog).toContain('aria-labelledby="node-editor-title"');
    expect(html).toMatch(
      /<h2 id="node-editor-title"[^>]*>Edit device alpha<\/h2>/,
    );
    expect(html).toContain('builder-inspector--editor');
    expect(html).toContain('aria-labelledby="node-editor-title"');
    expect(html).toContain('data-testid="node-editor-cancel"');
    expect(html).toContain('data-testid="node-editor-apply"');
    expect(textOf(html)).toContain('Cancel Apply');
    // Its own status region, which the page's cannot replace in a modal
    // dialog, and its error region, both there from the start.
    expect(html).toMatch(/<p class="builder-visually-hidden" role="status"/);
    expect(html).toContain('id="node-editor-error"');
    // The question it asks before dropping changes is there, not shown.
    expect(html).toContain('Discard changes to this node?');
  });

  // A device's form has Expand all, Collapse all and a table of contents,
  // which the browser fills once the form is drawn.
  test("a device's sections have Expand all, Collapse all and a table of contents", async () => {
    const { html } = await renderDialog(({ alpha }) => alpha.id);
    const [toggle] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="node-editor-toc-toggle"'),
    );

    expect(toggle).toContain('aria-controls="node-editor-toc"');
    expect(toggle).toContain('aria-expanded="false"');
    expect(html).toMatch(
      /<nav id="node-editor-toc"[^>]*aria-label="Table of contents"/,
    );
    expect(html).toMatch(
      /data-testid="node-editor-expand-all"[^>]*>\s*Expand all\s*</,
    );
    expect(html).toMatch(
      /data-testid="node-editor-collapse-all"[^>]*>\s*Collapse all\s*</,
    );
    // Before the fields, after the title.
    expect(html.indexOf('node-editor-collapse-all')).toBeLessThan(
      html.indexOf('builder-inspector--editor'),
    );

    const { html: network } = await renderDialog(({ sw }) => sw.id);

    expect(network).not.toContain('node-editor-toc');
    expect(network).not.toContain('node-editor-expand-all');
  });

  // Search fields, left of Expand all, for any node. Its count shows only
  // while it holds a search.
  test('has a search of the fields before Expand all', async () => {
    const { html } = await renderDialog(({ alpha }) => alpha.id);
    const [search] = tags(html, 'input').filter((tag) =>
      tag.includes('data-testid="node-editor-search"'),
    );

    expect(search).toContain('type="search"');
    expect(search).toContain('aria-label="Search fields"');
    expect(search).toContain('placeholder="Field name or path"');
    expect(search).not.toContain('aria-describedby');
    expect(html.indexOf('node-editor-toc-toggle')).toBeLessThan(
      html.indexOf('node-editor-search'),
    );
    expect(html.indexOf('node-editor-search')).toBeLessThan(
      html.indexOf('node-editor-expand-all'),
    );
    expect(html).not.toContain('node-editor-search-results');
    expect(html).not.toContain('is-searching');

    const { html: network } = await renderDialog(({ sw }) => sw.id);

    expect(network).toContain('data-testid="node-editor-search"');
  });

  // The kinds of node the Inspector edits, besides a device, and what the
  // dialog's title calls each.
  const KINDS = [
    { spec: { kind: 'switch' }, title: 'Edit network EXP' },
    { spec: { kind: 'group', title: 'Rack' }, title: 'Edit group Rack' },
    { spec: { kind: 'note', text: 'Check the PLCs' }, title: 'Edit note' },
    { spec: { kind: 'shape', label: 'DMZ' }, title: 'Edit rectangle DMZ' },
    { spec: { kind: 'icon', label: 'Internet' }, title: 'Edit icon Internet' },
    {
      spec: {
        kind: 'line',
        label: 'uplink',
        points: [
          { x: 0, y: 0 },
          { x: 80, y: 10 },
        ],
      },
      title: 'Edit line uplink',
    },
  ];

  test.each(KINDS)(
    'opens for the $spec.kind, named for it, with its form and Apply',
    async ({ spec, title }) => {
      const { html } = await renderDialog(
        ({ sw, added }) => (spec.kind === 'switch' ? sw.id : added.id),
        (store) => {
          if (spec.kind === 'switch') {
            return {};
          }

          const { doc, node } = addNode(store.doc, {
            ...spec,
            position: { x: 400, y: 400 },
          });

          store.commit(doc, '');

          return { added: node };
        },
      );

      expect(html).toContain(`>${title}</h2>`);
      expect(html).toContain('builder-inspector--editor');
      expect(html).toContain('<form');
      expect(html).toContain('data-testid="node-editor-apply"');
      expect(html).not.toContain('data-testid="node-editor-gone"');
    },
  );

  test('a read-only draft opens it read only, with Close only', async () => {
    const { html } = await renderDialog(
      ({ alpha }) => alpha.id,
      (store) => {
        store.readOnly = true;
      },
    );

    expect(html).toContain('>Device alpha</h2>');
    expect(html).not.toContain('data-testid="node-editor-apply"');
    expect(textOf(tags(html, 'button').join(''))).not.toContain('Apply');
    expect(html).toMatch(/data-testid="node-editor-cancel"[^>]*>\s*Close\s*</);
  });

  test('a device of an included topology opens read only, as the Inspector’s lock says', async () => {
    const { html } = await renderDialog(
      ({ alpha }) => alpha.id,
      (store, { alpha }) => {
        const doc = JSON.parse(JSON.stringify(store.doc));

        findNode(doc, alpha.id).device.includedFrom = 'plant-base';
        store.commit(doc, '');

        return {};
      },
    );

    expect(html).toContain('>Device alpha</h2>');
    expect(html).not.toContain('data-testid="node-editor-apply"');
    expect(html).toMatch(/data-testid="node-editor-cancel"[^>]*>\s*Close\s*</);
    expect(html).toMatch(
      /id="node-editor-included-note"[^>]*>\s*Included topology plant-base defines this device/,
    );
  });

  test('a node that is gone says so', async () => {
    const { html } = await renderDialog(() => 'no-such-node');

    expect(html).toContain('data-testid="node-editor-gone"');
    expect(html).not.toContain('builder-inspector--editor');
    expect(html).not.toContain('data-testid="node-editor-apply"');
  });
});
