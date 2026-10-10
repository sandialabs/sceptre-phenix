import { Buffer } from 'node:buffer';
import { brotliDecompressSync, gunzipSync } from 'node:zlib';

import { describe, expect, test } from 'vitest';

import builderAssets, {
  BUILDER_ASSET_LIST,
  DEFAULT_BROTLI_QUALITY,
  brotliQuality,
  builderFiles,
  encodings,
} from '../../plugins/builder-assets.js';

const VIEW = '/app/src/views/Builder.vue';

// Script that compresses well.
const SCRIPT = "postMessage({ layout: 'layered' });\n".repeat(200);

// Script varied enough that each Brotli quality compresses it differently.
function variedScript() {
  const lines = [];
  for (let i = 0; i < 400; i += 1) {
    lines.push(`const v${i} = '${((i * 7919) % 104729).toString(36)}';\n`);
  }

  return lines.join('');
}

function chunk(fileName, fields = {}) {
  const { css = [], assets = [], ...rest } = fields;

  return {
    type: 'chunk',
    fileName,
    code: SCRIPT,
    facadeModuleId: null,
    isEntry: false,
    imports: [],
    dynamicImports: [],
    viteMetadata: {
      importedCss: new Set(css),
      importedAssets: new Set(assets),
    },
    ...rest,
  };
}

function asset(fileName, source = SCRIPT) {
  return { type: 'asset', fileName, source };
}

// A bundle shaped as Vite builds it: Builder's view shares index.html's
// chunk, a chunk Configs also imports, and a chunk index.html loads lazily
// itself. It loads the ELK worker by URL: a chunk whose one asset is the
// worker. It starts the Auto-group pattern worker through a chunk that
// names the worker's file in its code, which no chunk lists as an asset.
function bundle() {
  const outputs = [
    chunk('assets/index-A.js', {
      isEntry: true,
      facadeModuleId: '/app/index.html',
      dynamicImports: [
        'assets/Configs-A.js',
        'assets/Builder-A.js',
        'assets/autosave-A.js',
      ],
      css: ['assets/index-A.css'],
      assets: ['assets/banner-A.png'],
    }),
    chunk('assets/Configs-A.js', {
      facadeModuleId: '/app/src/views/Configs.vue',
      imports: ['assets/index-A.js', 'assets/shared-A.js'],
    }),
    chunk('assets/autosave-A.js', {
      imports: ['assets/index-A.js'],
    }),
    chunk('assets/shared-A.js'),
    chunk('assets/Builder-A.js', {
      facadeModuleId: VIEW,
      imports: [
        'assets/index-A.js',
        'assets/autosave-A.js',
        'assets/shared-A.js',
        'assets/sampler-A.js',
      ],
      dynamicImports: [
        'assets/elk-api-A.js',
        'assets/elk-worker.min-A.js',
        'assets/groupingWorker-A.js',
      ],
      css: ['assets/Builder-A.css'],
    }),
    chunk('assets/groupingWorker-A.js', {
      code: `${SCRIPT}new Worker("/assets/groupingWorker-B.js");`,
    }),
    asset('assets/groupingWorker-B.js'),
    // A script asset no chunk names is not Builder's.
    asset('assets/other-worker-B.js'),
    chunk('assets/sampler-A.js'),
    chunk('assets/elk-api-A.js', { imports: ['assets/shared-A.js'] }),
    chunk('assets/elk-worker.min-A.js', {
      code: 'export default "/assets/elk-worker.min-B.js";',
      assets: ['assets/elk-worker.min-B.js'],
    }),
    asset('assets/elk-worker.min-B.js', new TextEncoder().encode(SCRIPT)),
    asset('assets/index-A.css', 'body { margin: 0 }'),
    asset('assets/Builder-A.css', '.builder { display: flex }\n'.repeat(50)),
    asset('assets/banner-A.png', new Uint8Array([0x89, 0x50, 0x4e, 0x47])),
  ];

  return Object.fromEntries(outputs.map((out) => [out.fileName, out]));
}

const BUILDER_FILES = [
  'assets/Builder-A.css',
  'assets/Builder-A.js',
  'assets/elk-api-A.js',
  'assets/elk-worker.min-A.js',
  'assets/elk-worker.min-B.js',
  'assets/groupingWorker-A.js',
  'assets/groupingWorker-B.js',
  'assets/sampler-A.js',
];

// Runs the plugin on a bundle, as a build does, in the environment env, and
// returns what it emitted, by name.
function generate(input, { view = VIEW, env = {} } = {}) {
  const emitted = {};
  const context = {
    emitFile({ type, fileName, source }) {
      expect(type).toBe('asset');
      expect(emitted).not.toHaveProperty(fileName);
      emitted[fileName] = source;
    },
    error(message) {
      throw new Error(message);
    },
  };

  const plugin = builderAssets({ view, env });

  plugin.buildStart.call(context);
  plugin.generateBundle.handler.call(context, {}, input);

  return emitted;
}

function contentOf(out) {
  return Buffer.from(out.type === 'chunk' ? out.code : out.source);
}

describe('builderFiles', () => {
  test('lists the files only Builder loads', () => {
    expect(builderFiles(bundle(), VIEW)).toEqual(BUILDER_FILES);
  });

  test('matches the view by its path', () => {
    expect(
      builderFiles(bundle(), '/app/src/views/../views/Builder.vue'),
    ).toEqual(BUILDER_FILES);
    expect(builderFiles(bundle(), '/app/src/views/Build.vue')).toBeNull();
  });

  test('leaves out what the rest of the UI loads through other chunks', () => {
    const input = bundle();
    input['assets/Configs-A.js'].dynamicImports = ['assets/elk-api-A.js'];

    expect(builderFiles(input, VIEW)).not.toContain('assets/elk-api-A.js');
  });

  test('lists a worker a Builder chunk starts, unless another page starts it too', () => {
    expect(builderFiles(bundle(), VIEW)).toContain(
      'assets/groupingWorker-B.js',
    );
    expect(builderFiles(bundle(), VIEW)).not.toContain(
      'assets/other-worker-B.js',
    );

    const input = bundle();
    input['assets/Configs-A.js'].code =
      'new Worker("/assets/groupingWorker-B.js")';

    expect(builderFiles(input, VIEW)).not.toContain(
      'assets/groupingWorker-B.js',
    );
  });
});

describe('the builder-assets plugin', () => {
  test('runs after Vite has finished the chunks, and only in a build', () => {
    const plugin = builderAssets({ view: VIEW });

    expect(plugin.apply).toBe('build');
    expect(plugin.generateBundle.order).toBe('post');
  });

  test('lists the files beside index.html', () => {
    const emitted = generate(bundle());

    expect(JSON.parse(emitted[BUILDER_ASSET_LIST])).toEqual(BUILDER_FILES);
  });

  test('writes the Brotli and gzip copies of each file', () => {
    const input = bundle();
    const emitted = generate(input);

    for (const file of BUILDER_FILES) {
      if (file === 'assets/elk-worker.min-A.js') {
        continue;
      }
      const content = contentOf(input[file]);

      expect(emitted[`${file}.br`].length).toBeLessThan(content.length);
      expect(brotliDecompressSync(emitted[`${file}.br`])).toEqual(content);
      expect(gunzipSync(emitted[`${file}.gz`])).toEqual(content);
    }
  });

  test('leaves out a copy no smaller than its file, and copies nothing else', () => {
    const emitted = generate(bundle());
    const copies = Object.keys(emitted).filter(
      (name) => name !== BUILDER_ASSET_LIST,
    );

    expect(copies.sort()).toEqual(
      BUILDER_FILES.filter((file) => file !== 'assets/elk-worker.min-A.js')
        .flatMap((file) => [`${file}.br`, `${file}.gz`])
        .sort(),
    );
  });

  test('fails the build without a chunk for the view', () => {
    expect(() =>
      generate(bundle(), { view: '/app/src/views/Build.vue' }),
    ).toThrow(/no chunk for \/app\/src\/views\/Build\.vue/);
  });
});

describe('the Brotli quality', () => {
  test('is 9 without PHENIX_BROTLI_QUALITY, else the whole number from 0 to 11 it names', () => {
    expect(DEFAULT_BROTLI_QUALITY).toBe(9);
    expect(brotliQuality({})).toBe(9);
    for (const quality of [0, 9, 10, 11]) {
      const env = { PHENIX_BROTLI_QUALITY: `${quality}` };

      expect(brotliQuality(env)).toBe(quality);
    }
    for (const value of ['', ' 9', '09', '9.5', '-1', '12', 'max']) {
      expect(brotliQuality({ PHENIX_BROTLI_QUALITY: value })).toBeNull();
    }
  });

  test('compresses at 9 by default, and at the quality PHENIX_BROTLI_QUALITY sets', () => {
    const input = bundle();
    input['assets/Builder-A.js'].code = variedScript();
    const content = contentOf(input['assets/Builder-A.js']);
    const at = (quality) => encodings(quality)['.br'](content);

    expect(at(11)).not.toEqual(at(9));
    expect(generate(input)['assets/Builder-A.js.br']).toEqual(at(9));

    const emitted = generate(input, { env: { PHENIX_BROTLI_QUALITY: '11' } });

    expect(emitted['assets/Builder-A.js.br']).toEqual(at(11));
    expect(brotliDecompressSync(at(11))).toEqual(content);
  });

  test('fails the build on any other PHENIX_BROTLI_QUALITY', () => {
    for (const value of ['', '12', 'max']) {
      const env = { PHENIX_BROTLI_QUALITY: value };
      const message = `PHENIX_BROTLI_QUALITY must be a whole number from 0 to 11, not "${value}".`;

      expect(() => generate(bundle(), { env })).toThrow(message);
    }
  });
});
