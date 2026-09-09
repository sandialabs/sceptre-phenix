import { describe, expect, test, vi } from 'vitest';
import YAML from 'js-yaml';

import {
  computeExportViewport,
  documentBounds,
  exportFileName,
  exportImage,
  IMAGE_PADDING,
  saveText,
  toJSONString,
  toYAMLString,
} from '@/builder/exporters.js';
import { parseDocument } from '@/builder/decode.js';

import { sampleDocument } from './fixtures.js';

describe('document export', () => {
  test('JSON exports re-import unchanged', () => {
    const { doc } = sampleDocument();
    const text = toJSONString(doc);

    expect(parseDocument(JSON.parse(text))).toEqual(doc);
  });

  test('YAML exports re-import unchanged', () => {
    const { doc } = sampleDocument();

    expect(parseDocument(YAML.load(toYAMLString(doc)))).toEqual(doc);
  });

  test('file names are filesystem safe', () => {
    expect(exportFileName({ name: 'My Topology!' }, 'json')).toBe(
      'my-topology.json',
    );
    expect(exportFileName({}, 'png')).toBe('topology.png');
  });
});

describe('image export geometry', () => {
  test('bounds cover every node, not the visible viewport', () => {
    const { doc } = sampleDocument();
    const bounds = documentBounds(doc);
    const xs = doc.nodes.map((node) => node.position.x);

    expect(bounds.x).toBeLessThanOrEqual(Math.min(...xs) - IMAGE_PADDING + 1);
    expect(bounds.width).toBeGreaterThan(0);
    expect(bounds.height).toBeGreaterThan(0);
  });

  test('an empty document still exports a usable canvas', () => {
    expect(documentBounds({ nodes: [] })).toEqual({
      x: 0,
      y: 0,
      width: 320,
      height: 240,
    });
  });

  test('the viewport transform fits the bounds', () => {
    const viewport = computeExportViewport({
      x: -100,
      y: -50,
      width: 800,
      height: 400,
    });

    expect(viewport.zoom).toBeLessThanOrEqual(2);
    expect(viewport.transform).toContain('scale(');
    expect(viewport.x).toBe(100 * viewport.zoom);
  });

  test('large diagrams are scaled down rather than clipped', () => {
    const viewport = computeExportViewport(
      { x: 0, y: 0, width: 20000, height: 400 },
      { maxWidth: 4096 },
    );

    expect(viewport.width).toBeLessThanOrEqual(4096);
    expect(viewport.zoom).toBeLessThan(1);
  });
});

describe('savers', () => {
  test('image export renders the whole diagram through the injected renderer', async () => {
    const { doc } = sampleDocument();
    const toPng = vi.fn(async () => 'data:image/png;base64,x');
    const saveAs = vi.fn();

    const url = await exportImage({
      element: {},
      doc,
      format: 'png',
      toPng,
      saveAs,
      backgroundColor: '#fff',
    });

    expect(url).toContain('data:image/png');
    expect(toPng).toHaveBeenCalledTimes(1);

    const [, options] = toPng.mock.calls[0];

    expect(options.width).toBe(
      computeExportViewport(documentBounds(doc)).width,
    );
    expect(saveAs).toHaveBeenCalledWith(url, 'sample.png');
  });

  test('image export refuses without a canvas element', async () => {
    await expect(exportImage({ doc: {}, toPng: vi.fn() })).rejects.toThrow(
      /No canvas element/,
    );
  });

  test('text export builds a blob with a charset', () => {
    const saveAs = vi.fn();

    class FakeBlob {
      constructor(parts, options) {
        this.parts = parts;
        this.options = options;
      }
    }

    const blob = saveText({
      text: '{}',
      mime: 'application/json',
      fileName: 'a.json',
      saveAs,
      BlobCtor: FakeBlob,
    });

    expect(blob.options.type).toBe('application/json;charset=utf-8');
    expect(saveAs).toHaveBeenCalledWith(blob, 'a.json');
  });
});
