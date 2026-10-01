import { Buffer } from 'node:buffer';
import { brotliDecompressSync, gunzipSync } from 'node:zlib';

import { describe, expect, test } from 'vitest';

import builderV2Assets, {
  BUILDER_V2_ASSET_LIST,
  builderV2Files,
} from '../../plugins/builder-v2-assets.js';

const VIEW = '/app/src/views/BuilderV2.vue';

// Script that compresses well.
const SCRIPT = "postMessage({ layout: 'layered' });\n".repeat(200);

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

// A bundle shaped as Vite builds it: Builder v2's view shares index.html's
// chunk, a chunk Configs also imports, and a chunk index.html loads lazily
// itself. It loads the ELK worker by URL: a chunk whose one asset is the
// worker.
function bundle() {
  const outputs = [
    chunk('assets/index-A.js', {
      isEntry: true,
      facadeModuleId: '/app/index.html',
      dynamicImports: [
        'assets/Configs-A.js',
        'assets/BuilderV2-A.js',
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
    chunk('assets/BuilderV2-A.js', {
      facadeModuleId: VIEW,
      imports: [
        'assets/index-A.js',
        'assets/autosave-A.js',
        'assets/shared-A.js',
        'assets/sampler-A.js',
      ],
      dynamicImports: ['assets/elk-api-A.js', 'assets/elk-worker.min-A.js'],
      css: ['assets/BuilderV2-A.css'],
    }),
    chunk('assets/sampler-A.js'),
    chunk('assets/elk-api-A.js', { imports: ['assets/shared-A.js'] }),
    chunk('assets/elk-worker.min-A.js', {
      code: 'export default "/assets/elk-worker.min-B.js";',
      assets: ['assets/elk-worker.min-B.js'],
    }),
    asset('assets/elk-worker.min-B.js', new TextEncoder().encode(SCRIPT)),
    asset('assets/index-A.css', 'body { margin: 0 }'),
    asset('assets/BuilderV2-A.css', '.builder { display: flex }\n'.repeat(50)),
    asset('assets/banner-A.png', new Uint8Array([0x89, 0x50, 0x4e, 0x47])),
  ];

  return Object.fromEntries(outputs.map((out) => [out.fileName, out]));
}

const BUILDER_V2_FILES = [
  'assets/BuilderV2-A.css',
  'assets/BuilderV2-A.js',
  'assets/elk-api-A.js',
  'assets/elk-worker.min-A.js',
  'assets/elk-worker.min-B.js',
  'assets/sampler-A.js',
];

// Runs the plugin on a bundle, and returns what it emitted, by name.
function generate(input, view = VIEW) {
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

  builderV2Assets({ view }).generateBundle.handler.call(context, {}, input);

  return emitted;
}

function contentOf(out) {
  return Buffer.from(out.type === 'chunk' ? out.code : out.source);
}

describe('builderV2Files', () => {
  test('lists the files only Builder v2 loads', () => {
    expect(builderV2Files(bundle(), VIEW)).toEqual(BUILDER_V2_FILES);
  });

  test('matches the view by its path', () => {
    expect(
      builderV2Files(bundle(), '/app/src/views/../views/BuilderV2.vue'),
    ).toEqual(BUILDER_V2_FILES);
    expect(builderV2Files(bundle(), '/app/src/views/Builder.vue')).toBeNull();
  });

  test('leaves out what the rest of the UI loads through other chunks', () => {
    const input = bundle();
    input['assets/Configs-A.js'].dynamicImports = ['assets/elk-api-A.js'];

    expect(builderV2Files(input, VIEW)).not.toContain('assets/elk-api-A.js');
  });
});

describe('the builder-v2-assets plugin', () => {
  test('runs after Vite has finished the chunks, and only in a build', () => {
    const plugin = builderV2Assets({ view: VIEW });

    expect(plugin.apply).toBe('build');
    expect(plugin.generateBundle.order).toBe('post');
  });

  test('lists the files beside index.html', () => {
    const emitted = generate(bundle());

    expect(JSON.parse(emitted[BUILDER_V2_ASSET_LIST])).toEqual(
      BUILDER_V2_FILES,
    );
  });

  test('writes the Brotli and gzip copies of each file', () => {
    const input = bundle();
    const emitted = generate(input);

    for (const file of BUILDER_V2_FILES) {
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
      (name) => name !== BUILDER_V2_ASSET_LIST,
    );

    expect(copies.sort()).toEqual(
      BUILDER_V2_FILES.filter((file) => file !== 'assets/elk-worker.min-A.js')
        .flatMap((file) => [`${file}.br`, `${file}.gz`])
        .sort(),
    );
  });

  test('fails the build without a chunk for the view', () => {
    expect(() => generate(bundle(), '/app/src/views/Builder.vue')).toThrow(
      /no chunk for \/app\/src\/views\/Builder\.vue/,
    );
  });
});
