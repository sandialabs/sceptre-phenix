// The Download dialog, rendered on the server: its buttons in two rows, and
// the link to the Gephi project. Starting a download as the dialog opens
// (a palette command's format) needs a browser, so it is left to the
// Playwright specs.

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

import DownloadDialog from '@/components/builder/dialogs/DownloadDialog.vue';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

async function render(props = {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(DownloadDialog, props) });

  app.use(pinia);
  useBuilderStore(pinia).doc = sampleDocument().doc;

  return renderToString(app);
}

// What an element holds, as text: tags out, white space as one space.
function textOf(html) {
  return html
    .replace(/<[^>]*>/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

describe('the Download dialog', () => {
  test('has its formats in two rows: documents and config, then pictures and graph', async () => {
    const html = await render();
    const rows = [
      ...html.matchAll(
        /<div class="builder-download__actions"[^>]*>([\s\S]*?)<\/div>/g,
      ),
    ].map(([, row]) =>
      [...row.matchAll(/<button\b[^>]*data-testid="([a-z-]+)"/g)].map(
        (match) => match[1],
      ),
    );

    expect(rows).toEqual([
      ['download-json', 'download-yaml', 'download-topology-yaml'],
      ['download-png', 'download-svg', 'download-gexf'],
    ]);

    const names = [
      ...html.matchAll(
        /<button\b[^>]*data-testid="download-[a-z-]+"[^>]*>([\s\S]*?)<\/button>/g,
      ),
    ].map(([, inner]) => textOf(inner));

    // The Builder package's button comes after the two rows, with its
    // ticks and its format.
    expect(names).toEqual([
      'Builder JSON',
      'Builder YAML',
      'Topology YAML',
      'PNG',
      'SVG',
      'Gephi (GEXF)',
      'Builder package',
    ]);
  });

  test('offers a Builder package of JSON or YAML, every section unticked', async () => {
    const html = await render();
    const [, fieldset] = html.match(
      /<fieldset\b[^>]*builder-download__package[^>]*>([\s\S]*?)<\/fieldset>/,
    );
    const ticks = [
      ...fieldset.matchAll(
        /<label\b[^>]*>\s*<input\b([^>]*)>([\s\S]*?)<\/label>/g,
      ),
    ].map(([, attributes, label]) => ({
      testId: attributes.match(/data-testid="([a-z-]+)"/)[1],
      checked: /\schecked\b/.test(attributes),
      label: textOf(label),
    }));

    expect(textOf(fieldset)).toMatch(
      /^Builder package One file with the diagram/,
    );
    expect(ticks).toEqual([
      {
        testId: 'download-package-scenarios',
        checked: false,
        label: 'Scenario configs',
      },
      {
        testId: 'download-package-topologies',
        checked: false,
        label: 'Included topologies',
      },
      {
        testId: 'download-package-icons',
        checked: false,
        label: 'Custom icons',
      },
      {
        testId: 'download-package-images',
        checked: false,
        label: 'Disk-image requirements',
      },
    ]);
    expect(fieldset).toMatch(
      /<label for="download-package-format"[^>]*>Package format<\/label>/,
    );
    expect(
      [...fieldset.matchAll(/<option value="([a-z]+)"/g)].map(
        ([, value]) => value,
      ),
    ).toEqual(['json', 'yaml']);
  });

  test('links Gephi in its hint to the project, in a new tab, and says so', async () => {
    const html = await render();
    const [, hint] = html.match(
      /<p id="download-gexf-hint"[^>]*>([\s\S]*?)<\/p>/,
    );
    const [link, attributes, inner] = hint.match(/<a\b([^>]*)>([\s\S]*?)<\/a>/);

    expect(attributes).toContain('href="https://gephi.org/"');
    expect(attributes).toContain('target="_blank"');
    expect(attributes).toContain('rel="noopener noreferrer"');
    expect(attributes).toContain('data-testid="download-gephi-link"');
    // Its name: the word, then what a press does, for screen readers; the
    // mark beside it is only a picture.
    expect(textOf(inner)).toBe('Gephi (opens in a new tab)');
    expect(inner).toMatch(
      /<span class="builder-visually-hidden"[^>]*> \(opens in a new tab\)<\/span>/,
    );
    expect(inner).toMatch(/<svg\b[^>]*builder-icon--external[^>]*>/);
    expect(inner).toMatch(/<svg\b[^>]*aria-hidden="true"/);

    // The hint reads as one sentence around the link, and still describes
    // the Gephi (GEXF) button.
    expect(textOf(hint.replace(link, 'Gephi'))).toBe(
      'Gephi (GEXF): the devices, networks and connections, with their ' +
        'settings and scenario apps, as a graph to analyze in Gephi. The ' +
        'Builder cannot open it.',
    );
    expect(html).toMatch(
      /<button\b[^>]*data-testid="download-gexf"[^>]*aria-describedby="download-gexf-hint"/,
    );
    // The one link of the dialog.
    expect(html.match(/<a\b/g)).toHaveLength(1);
  });

  test('renders the same whatever format it is asked to start', async () => {
    const plain = await render();

    for (const start of ['json', 'yaml', 'topology', 'png', 'svg', 'gexf']) {
      expect(await render({ start }), start).toBe(plain);
    }
  });
});
