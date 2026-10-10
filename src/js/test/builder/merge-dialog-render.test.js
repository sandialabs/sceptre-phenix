// The merge dialog as the server renders it: a group of two radio buttons
// per clashing field, neither chosen, and Save merged unavailable until each
// has a choice. Choosing, saving and focus need a browser, and are in
// builder-sharing.spec.js and builder-persistence.spec.js.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import MergeDialog from '@/components/builder/dialogs/MergeDialog.vue';
import { addNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

function renamed(doc, id, name) {
  return {
    ...doc,
    nodes: doc.nodes.map((node) =>
      node.id === id
        ? {
            ...node,
            label: name,
            device: {
              ...node.device,
              hostname: name,
              spec: {
                ...node.device.spec,
                general: { ...node.device.spec.general, hostname: name },
              },
            },
          }
        : node,
    ),
  };
}

// The dialog over a review of `mine` and `theirs`, both changed from the
// sample diagram, which bob saved. With `opened`, what the dialog does once
// it has opened (the server never runs onMounted) is run before it renders.
function render(change, { opened = false } = {}) {
  const pinia = createPinia();

  setActivePinia(pinia);

  const store = useBuilderStore();
  const { doc: base, alpha, bravo } = sampleDocument();
  const { mine, theirs } = change(base, alpha, bravo);

  store.doc = mine;
  store.merge = {
    status: 'review',
    from: 'bob',
    reason: '',
    clashes: 1,
    base,
    server: { document: theirs },
  };

  const app = createSSRApp({ render: () => h(MergeDialog) });

  app.use(pinia);

  if (opened) {
    app.mixin({
      serverPrefetch() {
        if (this.$.type === MergeDialog && !this.$.setupState.clashes.length) {
          this.$.setupState.explain();
        }
      },
    });
  }

  return renderToString(app);
}

// The tag of the element with that test id.
function tagOf(html, testId) {
  return html.match(new RegExp(`<[a-z]+[^>]*data-testid="${testId}"[^>]*>`))[0];
}

describe('the merge dialog', () => {
  test('lists each clash with Keep mine and Keep theirs, none chosen', async () => {
    const html = await render((base, alpha, bravo) => ({
      mine: renamed(renamed(base, alpha.id, 'mine'), bravo.id, 'bravo-mine'),
      theirs: renamed(
        renamed(base, alpha.id, 'theirs'),
        bravo.id,
        'bravo-theirs',
      ),
    }));

    expect(html).toContain('Merge changes from bob');
    expect(html).toMatch(/<legend[^>]*>alpha name<\/legend>/);
    expect(html).toMatch(/<legend[^>]*>bravo name<\/legend>/);
    expect(html).toContain('You and they both changed alpha name.');
    expect(html).toMatch(/Keep mine: mine\s*<\/label>/);
    expect(html).toMatch(/Keep theirs: theirs\s*<\/label>/);
    expect(html).not.toContain('checked');
    expect(html).toContain('0 of 2 chosen');
    expect(html).toMatch(
      /data-testid="merge-save"[^>]*aria-disabled="true"|aria-disabled="true"[^>]*data-testid="merge-save"/,
    );
    expect(html).toContain('Keep all mine');
    expect(html).toContain('Keep all theirs');
  });

  test('a deleted device offers to delete it or keep it with the other changes', async () => {
    const html = await render((base, _, bravo) => ({
      mine: {
        ...base,
        nodes: base.nodes.filter((node) => node.id !== bravo.id),
      },
      theirs: renamed(base, bravo.id, 'bravo-2'),
    }));

    expect(html).toMatch(/<legend[^>]*>bravo<\/legend>/);
    expect(html).toContain('You deleted bravo; they changed its name.');
    expect(html).toMatch(/Keep mine: delete bravo\s*<\/label>/);
    expect(html).toMatch(
      /Keep theirs: keep bravo with their changes\s*<\/label>/,
    );
  });

  test('with nothing to choose, lists why the merged diagram is refused, offers only Cancel, and Save merged is unavailable', async () => {
    // Both add a device charlie: no field clashes, but the merged diagram
    // has two devices of one hostname, which the strict check refuses.
    const charlie = (doc) =>
      addNode(doc, { kind: 'device', hostname: 'charlie' }).doc;
    const html = await render(
      (base) => ({ mine: charlie(base), theirs: charlie(base) }),
      { opened: true },
    );
    const save = tagOf(html, 'merge-save');

    expect(html).toMatch(
      /Nothing you and bob changed clashes, so there is nothing to\s+choose, but the merged diagram cannot be saved as it is\./,
    );
    expect(html).not.toContain('Keep all mine');
    expect(html).not.toContain('merge-count');
    expect(html).toContain(
      'The merged diagram cannot be saved because of the problems listed below. Cancel offers saving your history as a new draft or discarding it.',
    );
    expect(html).not.toContain('Choose otherwise');
    expect(html).toMatch(
      /data-testid="merge-issues"[^>]*>[\s\S]*duplicate hostname &quot;charlie&quot;/,
    );
    expect(save).toContain('aria-disabled="true"');
    expect(save).toContain('aria-describedby="merge-error"');
  });

  test('with a clash, nothing is refused as it opens, and Save merged is described by the count', async () => {
    const html = await render(
      (base, alpha) => ({
        mine: renamed(base, alpha.id, 'mine'),
        theirs: renamed(base, alpha.id, 'theirs'),
      }),
      { opened: true },
    );

    expect(html).not.toContain('merge-issues');
    expect(tagOf(html, 'merge-save')).toContain(
      'aria-describedby="merge-count"',
    );
  });
});
