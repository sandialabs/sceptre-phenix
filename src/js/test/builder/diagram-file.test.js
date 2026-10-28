// The file Download saves for a diagram (diagramFile.js).

import { describe, expect, test, vi } from 'vitest';

import {
  diagramFile,
  downloadedDiagram,
  queuedDiagram,
} from '@/builder/diagramFile.js';

describe('the file Download saves', () => {
  const snapshot = (id) => ({ opId: id, kind: 'snapshot', commitId: id });
  const entry = (id, name) => ({
    id,
    snapshot: { metadata: { name }, nodes: [], edges: [] },
  });

  test("holds the diagram that a queued record's last change made current", () => {
    expect(
      queuedDiagram({
        entries: [entry('b', 'Lab b'), entry('c', 'Lab c')],
        queue: [
          snapshot('b'),
          snapshot('c'),
          { opId: 'u', kind: 'cursor', commitId: 'b' },
        ],
      }),
    ).toEqual(entry('b', 'Lab b').snapshot);
    // A move to a snapshot only the server holds leaves nothing here.
    expect(
      queuedDiagram({
        queue: [{ opId: 'v', kind: 'cursor', snapshotId: 's9' }],
      }),
    ).toBeNull();
  });

  test('is saved as Download saves JSON', () => {
    expect(diagramFile({ metadata: { name: '  Core Lab: v2! ' } })).toEqual({
      name: 'core-lab-v2.json',
      text: '{\n  "metadata": {\n    "name": "  Core Lab: v2! "\n  }\n}\n',
    });
    expect(diagramFile({}).name).toBe('topology.json');
  });

  // As Download does, the file carries every custom icon the diagram uses,
  // read from the library first, so it stands on its own.
  test('carries the custom icons the diagram uses', async () => {
    const node = (id, icon) => ({
      id,
      kind: 'device',
      device: { hostname: id, icon },
    });
    const icons = new Map();
    const iconLibrary = {
      ensureImages: vi.fn(async () => {
        icons.set('plc', { name: 'plc', data: 'cGxj' });
        icons.set('hmi', { name: 'hmi', data: 'aG1p' });
      }),
      lookup: (name) => icons.get(name.toLowerCase()) || null,
    };
    const doc = {
      metadata: { name: 'Icons lab' },
      nodes: [node('a', 'PLC'), node('b', 'hmi'), node('c', 'gone')],
      edges: [],
      icons: { hmi: { data: 'b3du' }, unused: { data: 'b3du' } },
    };

    const file = diagramFile(
      await downloadedDiagram(doc, { library: iconLibrary }),
    );

    // The images of the icons the nodes name are read first.
    expect(iconLibrary.ensureImages).toHaveBeenCalledExactlyOnceWith(
      new Set(['PLC', 'hmi', 'gone']),
    );
    expect(file.name).toBe('icons-lab.json');
    // Keyed by the names the nodes use: the diagram's own copy wins, and an
    // icon neither has, or one no node names, is left out.
    expect(JSON.parse(file.text).icons).toEqual({
      PLC: { data: 'cGxj' },
      hmi: { data: 'b3du' },
    });

    // A diagram that names no custom icon does not read the library.
    iconLibrary.ensureImages.mockClear();
    await expect(
      downloadedDiagram(
        { metadata: { name: 'Plain' } },
        { library: iconLibrary },
      ),
    ).resolves.toEqual({ metadata: { name: 'Plain' } });
    expect(iconLibrary.ensureImages).not.toHaveBeenCalled();
  });
});
