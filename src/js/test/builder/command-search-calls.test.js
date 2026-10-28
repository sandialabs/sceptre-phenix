import { expect, test, vi } from 'vitest';

import { createChoiceCache, paletteResults } from '@/builder/commandSearch.js';
import { VIEW_API, createCommandContext } from '@/builder/commands.js';
import { matchItem, matchName } from '@/builder/fuzzy.js';

import { sampleDocument, withTemplates } from './fixtures.js';

// The real matchers, counted.
vi.mock('@/builder/fuzzy.js', async (importOriginal) => {
  const fuzzy = await importOriginal();

  return {
    ...fuzzy,
    matchItem: vi.fn(fuzzy.matchItem),
    matchName: vi.fn(fuzzy.matchName),
  };
});

function context(size) {
  const { doc } = sampleDocument();
  const model = doc.nodes.find((node) => node.kind === 'device');

  for (let i = 0; i < size; i += 1) {
    doc.nodes.push({
      ...model,
      id: `d-${i}`,
      label: `host-${i}`,
      device: { ...model.device, hostname: `host-${i}` },
    });
  }

  const view = { editing: true, icons: [] };

  for (const name of VIEW_API) {
    view[name] ??= vi.fn();
  }

  return createCommandContext({
    store: withTemplates({
      doc,
      readOnly: false,
      selection: { nodes: [], edges: [] },
      drafts: { all: [] },
      documents: [],
    }),
    view,
  });
}

test('@ matches the name of each node once per query', () => {
  const ctx = context(200);
  const cache = createChoiceCache();
  const nodes = ctx.store.doc.nodes.length;

  for (const query of ['@h', '@host-1', '@ubuntu', '@zzz']) {
    vi.mocked(matchItem).mockClear();
    vi.mocked(matchName).mockClear();

    const results = paletteResults(ctx, { query, cache });
    const named = vi
      .mocked(matchName)
      .mock.results.filter(({ value }) => value).length;

    expect(matchName).toHaveBeenCalledTimes(nodes);
    // Only a node whose name did not match is matched again, without its
    // name.
    expect(matchItem).toHaveBeenCalledTimes(nodes - named);

    for (const [, , options] of vi.mocked(matchItem).mock.calls) {
      expect(options).toEqual({ name: false });
    }

    if (query === '@host-1') {
      expect(results.groups[0].items[0].title).toBe('host-1');
    }
  }
});
