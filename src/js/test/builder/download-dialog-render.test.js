// The Download dialog, rendered on the server: two rows of buttons and the
// draw.io and Gephi links. A download started as the dialog opens is left to
// Playwright.

import { describe, expect, test } from 'vitest';

import DownloadDialog from '@/components/builder/dialogs/DownloadDialog.vue';

import { sampleDocument, textOf } from './fixtures.js';
import { renderWithStore } from './render.js';

async function render(props = {}) {
  const { html } = await renderWithStore(DownloadDialog, {
    props,
    prepare(store) {
      store.doc = sampleDocument().doc;
    },
  });

  return html;
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
      ['download-png', 'download-svg', 'download-drawio', 'download-gexf'],
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
      'draw.io',
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
      /^Builder package The package is one file\. It holds the diagram/,
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
    // Its name: the word, then what a press does, for screen readers. The
    // mark beside it is only a picture.
    expect(textOf(inner)).toBe('Gephi (opens in a new tab)');
    expect(inner).toMatch(
      /<span class="builder-visually-hidden"[^>]*> \(opens in a new tab\)<\/span>/,
    );
    // The new-tab mark, not the external device's monitor.
    expect(inner).toMatch(/<svg\b[^>]*builder-icon--external-link\b[^>]*>/);
    expect(inner).not.toMatch(/builder-icon--external"/);
    expect(inner).toMatch(/<svg\b[^>]*aria-hidden="true"/);

    // The hint reads as one sentence around the link, and still describes
    // the Gephi (GEXF) button.
    expect(textOf(hint.replace(link, 'Gephi'))).toBe(
      'Gephi (GEXF): the devices, networks and connections, with their ' +
        'settings and scenario apps, as a graph to analyze in Gephi.',
    );
    expect(html).toMatch(
      /<button\b[^>]*data-testid="download-gexf"[^>]*aria-describedby="download-gexf-hint"/,
    );
    // The links of the dialog: draw.io's and Gephi's.
    expect(html.match(/<a\b/g)).toHaveLength(2);
  });

  test('Topology YAML has no hint, and no hint says the Builder cannot open a file', async () => {
    const html = await render();
    const [button] = html.match(
      /<button\b[^>]*data-testid="download-topology-yaml"[^>]*>/,
    );

    expect(button).not.toContain('aria-describedby');
    expect(html).not.toContain('download-topology-hint');
    expect(html).not.toContain('Publish would write');
    expect(html).not.toContain('cannot open');
  });

  test('links draw.io in its hint to the project, in a new tab', async () => {
    const html = await render();
    const [, hint] = html.match(
      /<p id="download-drawio-hint"[^>]*>([\s\S]*?)<\/p>/,
    );
    const [link, attributes, inner] = hint.match(/<a\b([^>]*)>([\s\S]*?)<\/a>/);

    expect(attributes).toContain('href="https://www.drawio.com/"');
    expect(attributes).toContain('target="_blank"');
    expect(attributes).toContain('rel="noopener noreferrer"');
    expect(attributes).toContain('data-testid="download-drawio-link"');
    expect(textOf(inner)).toBe('draw.io (opens in a new tab)');
    expect(inner).toMatch(/<svg\b[^>]*builder-icon--external-link\b[^>]*>/);
    expect(textOf(hint.replace(link, 'draw.io'))).toBe(
      "draw.io: the diagram, in the light theme's colors, as a drawing to " +
        'edit in draw.io.',
    );
    expect(html).toMatch(
      /<button\b[^>]*data-testid="download-drawio"[^>]*aria-describedby="download-drawio-hint"/,
    );
  });

  test('renders the same whatever format it is asked to start', async () => {
    const plain = await render();

    for (const start of [
      'json',
      'yaml',
      'topology',
      'png',
      'svg',
      'drawio',
      'gexf',
    ]) {
      expect(await render({ start }), start).toBe(plain);
    }
  });
});
