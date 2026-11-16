import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import builderPrefetch, {
  builderFileList,
  FILE_LIST_ID,
  fileListTag,
} from '../../plugins/builder-prefetch.js';

const ROOT = '/app';
const VIEW = '/app/src/views/Builder.vue';
const ELK_API = '/app/node_modules/elkjs/lib/elk-api.js';
const ELK_WORKER = '/app/node_modules/elkjs/lib/elk-worker.min.js?url';
const GROUPING_WORKER = '/app/src/builder/groupingWorker.js?worker';
const MODULES = [ELK_API, ELK_WORKER, GROUPING_WORKER];

function chunk(fileName, { css = [], assets = [], ...rest } = {}) {
  return {
    type: 'chunk',
    fileName,
    code: '',
    facadeModuleId: null,
    moduleIds: rest.facadeModuleId ? [rest.facadeModuleId] : [],
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

function asset(fileName, originalFileNames = []) {
  return { type: 'asset', fileName, source: '', originalFileNames };
}

// A bundle shaped as Vite builds it. Builder imports the entry, a chunk the
// entry also imports (so index.html preloads it), and a chain of its own. It
// imports ELK, its worker URL, the grouping worker and a picker lazily. The
// assets named in leave are not in it.
function bundle({ leave = [] } = {}) {
  const outputs = [
    chunk('assets/index-A.js', {
      isEntry: true,
      facadeModuleId: '/app/index.html',
      imports: ['assets/vendor-A.js'],
      dynamicImports: ['assets/Builder-A.js'],
      css: ['assets/index-A.css'],
    }),
    chunk('assets/vendor-A.js'),
    chunk('assets/Builder-A.js', {
      facadeModuleId: VIEW,
      imports: ['assets/index-A.js', 'assets/vendor-A.js', 'assets/forms-A.js'],
      dynamicImports: [
        'assets/elk-api-A.js',
        'assets/elk-worker.min-A.js',
        'assets/groupingWorker-A.js',
        'assets/picker-A.js',
      ],
      css: ['assets/Builder-A.css'],
    }),
    chunk('assets/forms-A.js', {
      imports: ['assets/ajv-A.js', 'assets/Builder-A.js'],
      css: ['assets/forms-A.css', 'assets/Builder-A.css'],
    }),
    chunk('assets/ajv-A.js', { imports: ['assets/forms-A.js'] }),
    chunk('assets/picker-A.js'),
    chunk('assets/elk-api-A.js', {
      facadeModuleId: ELK_API,
      imports: ['assets/vendor-A.js'],
    }),
    // The ?url module: a chunk that exports the URL of the file.
    chunk('assets/elk-worker.min-A.js', {
      facadeModuleId: ELK_WORKER,
      code: 'const e="/assets/elk-worker.min-B.js";export{e as default};',
      assets: ['assets/elk-worker.min-B.js'].filter(
        (file) => !leave.includes(file),
      ),
    }),
    asset('assets/elk-worker.min-B.js', [
      'node_modules/elkjs/lib/elk-worker.min.js',
    ]),
    // The ?worker module: a chunk that names the worker script, an asset.
    chunk('assets/groupingWorker-A.js', {
      facadeModuleId: GROUPING_WORKER,
      code: 'function e(r){return new Worker("/assets/groupingWorker-B.js")}',
    }),
    asset('assets/groupingWorker-B.js'),
  ];

  return Object.fromEntries(
    outputs
      .filter((out) => !leave.includes(out.fileName))
      .map((out) => [out.fileName, out]),
  );
}

// index.html as Vite writes it for the bundle, with the base given.
function indexHtml(base = '/') {
  return [
    '<!doctype html>',
    '<html lang="en-US">',
    '  <head>',
    `    <link rel="icon" href="${base}favicon.ico" />`,
    `    <script type="module" crossorigin src="${base}assets/index-A.js"></script>`,
    `    <link rel="modulepreload" crossorigin href="${base}assets/vendor-A.js">`,
    `    <link rel="stylesheet" crossorigin href="${base}assets/index-A.css">`,
    '  </head>',
    '  <body><div id="app"></div></body>',
    '</html>',
  ].join('\n');
}

const BUILDER_URLS = [
  '/assets/Builder-A.js',
  '/assets/Builder-A.css',
  '/assets/forms-A.js',
  '/assets/forms-A.css',
  '/assets/ajv-A.js',
];

const MODULE_URLS = [
  '/assets/elk-api-A.js',
  '/assets/elk-worker.min-A.js',
  '/assets/elk-worker.min-B.js',
  '/assets/groupingWorker-A.js',
  '/assets/groupingWorker-B.js',
];

function list(input = bundle(), { view = VIEW, modules = MODULES, base } = {}) {
  return builderFileList(
    input,
    { view, modules },
    { html: indexHtml(base), base, root: ROOT },
  );
}

describe('builderFileList', () => {
  test('lists the view chunk, its static imports in turn, and their styles', () => {
    expect(list(bundle(), { modules: [] })).toEqual(BUILDER_URLS);
  });

  test('then lists the files of each module, with the worker scripts they name', () => {
    expect(list()).toEqual([...BUILDER_URLS, ...MODULE_URLS]);
  });

  test('leaves out what index.html loads and what Builder imports lazily', () => {
    const urls = list();

    for (const url of [
      '/assets/index-A.js',
      '/assets/vendor-A.js',
      '/assets/index-A.css',
      '/assets/picker-A.js',
    ]) {
      expect(urls).not.toContain(url);
    }
  });

  test('puts the base before each file', () => {
    expect(list(bundle(), { base: '/phenix/' })).toEqual(
      [...BUILDER_URLS, ...MODULE_URLS].map((url) => `/phenix${url}`),
    );
  });

  test('matches the view and the modules by their paths', () => {
    expect(
      list(bundle(), {
        view: '/app/src/views/../views/Builder.vue',
        modules: ['/app/node_modules/elkjs/lib/../lib/elk-api.js'],
      }),
    ).toEqual([...BUILDER_URLS, '/assets/elk-api-A.js']);
  });

  test('finds a module in a chunk that it is not the facade of', () => {
    const input = bundle();
    input['assets/ajv-A.js'].moduleIds = ['/app/node_modules/ajv/index.js'];

    expect(
      list(input, { modules: ['/app/node_modules/ajv/index.js'] }),
    ).toEqual(BUILDER_URLS);
  });

  test('finds a module that the build emits as an asset by its source path', () => {
    expect(
      list(bundle(), {
        modules: ['/app/node_modules/elkjs/lib/elk-worker.min.js'],
      }),
    ).toEqual([...BUILDER_URLS, '/assets/elk-worker.min-B.js']);
  });

  test('fails without a chunk for the view', () => {
    expect(() => list(bundle(), { view: '/app/src/views/Build.vue' })).toThrow(
      /no chunk for \/app\/src\/views\/Build\.vue/,
    );
  });

  test('fails without a file for a module', () => {
    expect(() =>
      list(bundle(), { modules: ['/app/src/builder/missing.js?worker'] }),
    ).toThrow(/no file for \/app\/src\/builder\/missing\.js\?worker/);
  });

  test.each([
    ['assets/groupingWorker-B.js', GROUPING_WORKER],
    ['assets/elk-worker.min-B.js', ELK_WORKER],
  ])('fails without %s, which a module loads', (leave, id) => {
    expect(() => list(bundle({ leave: [leave] }))).toThrow(
      `The build has no file that ${id} loads`,
    );
  });

  test('lists each file of the graph once, unless index.html loads it', () => {
    // Every file the graph reaches, in the order of the list.
    const graph = [
      '/assets/Builder-A.js',
      '/assets/Builder-A.css',
      '/assets/index-A.js',
      '/assets/index-A.css',
      '/assets/vendor-A.js',
      '/assets/forms-A.js',
      '/assets/forms-A.css',
      '/assets/ajv-A.js',
      ...MODULE_URLS,
    ];

    fc.assert(
      fc.property(fc.subarray(graph), (loaded) => {
        const html = loaded
          .map((href) => `<link rel="modulepreload" href="${href}">`)
          .join('\n');
        const urls = builderFileList(
          bundle(),
          { view: VIEW, modules: MODULES },
          { html, root: ROOT },
        );

        expect(urls).toEqual(graph.filter((href) => !loaded.includes(href)));
      }),
      { numRuns: 100 },
    );
  });
});

describe('fileListTag', () => {
  test('is a JSON data element in the head', () => {
    expect(fileListTag(['/assets/a.js'])).toEqual({
      tag: 'script',
      attrs: { type: 'application/json', id: FILE_LIST_ID },
      children: '["/assets/a.js"]',
      injectTo: 'head',
    });
  });

  test('holds the URLs as JSON, with no "<" that could end the element', () => {
    fc.assert(
      fc.property(fc.array(fc.string()), (urls) => {
        const { children } = fileListTag(urls);

        expect(children).not.toContain('<');
        expect(JSON.parse(children)).toEqual(urls);
      }),
      { numRuns: 100 },
    );
  });
});

describe('the builder-prefetch plugin', () => {
  // Runs the plugin's index.html hook as a build does.
  function transform(input, { view = VIEW, base = '/' } = {}) {
    const plugin = builderPrefetch({ view, modules: MODULES });
    plugin.configResolved({ base, root: ROOT });

    return plugin.transformIndexHtml.handler(indexHtml(base), {
      bundle: input,
    });
  }

  test('runs after Vite adds its own tags, and only in a build', () => {
    const plugin = builderPrefetch({ view: VIEW });

    expect(plugin.apply).toBe('build');
    expect(plugin.transformIndexHtml.order).toBe('post');
  });

  test('adds only the list, as data, with the resolved base', () => {
    expect(transform(bundle())).toEqual([
      fileListTag([...BUILDER_URLS, ...MODULE_URLS]),
    ]);
    expect(transform(bundle(), { base: '/phenix/' })).toEqual([
      fileListTag(
        [...BUILDER_URLS, ...MODULE_URLS].map((url) => `/phenix${url}`),
      ),
    ]);
  });

  test('adds no prefetch tags', () => {
    for (const tag of transform(bundle())) {
      expect(tag.tag).not.toBe('link');
      expect(tag.attrs.rel).toBeUndefined();
    }
  });

  test('fails the build without a chunk for the view', () => {
    expect(() =>
      transform(bundle(), { view: '/app/src/views/Build.vue' }),
    ).toThrow(/no chunk for \/app\/src\/views\/Build\.vue/);
  });

  test('fails the build without the grouping worker script', () => {
    expect(() =>
      transform(bundle({ leave: ['assets/groupingWorker-B.js'] })),
    ).toThrow(`The build has no file that ${GROUPING_WORKER} loads`);
  });
});
