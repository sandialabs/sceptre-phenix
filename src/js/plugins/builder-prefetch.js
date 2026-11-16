// Writes into the built index.html, as JSON data, the URLs of the files that
// Builder loads first. After the page loads, prefetchListedFiles in
// src/utils/prefetch.js reads the list and prefetches the files.
// The router does not prefetch Builder's large chunk itself (see router.js).
import path from 'node:path';

import { viewChunkOf } from './builder-assets.js';

// The id of the data element. src/utils/prefetch.js reads the element with
// this id.
export const FILE_LIST_ID = 'builder-prefetch';

// The script and stylesheet URLs that the tags of html already load.
function loadedUrls(html) {
  const urls = new Set();
  for (const [tag] of html.matchAll(/<(?:script|link)\b[^>]*>/gi)) {
    const url = /\b(?:src|href)\s*=\s*["']([^"']+)["']/i.exec(tag);
    if (url) {
      urls.add(url[1]);
    }
  }

  return urls;
}

// A module id with its path made absolute and normal, and its query kept.
function moduleKey(id) {
  const [file, ...query] = id.split('?');
  return [path.resolve(file), ...query].join('?');
}

// The chunk of the bundle that has the module id, as its facade first.
function chunkOfModule(bundle, id) {
  const key = moduleKey(id);
  const chunks = Object.values(bundle).filter((out) => out.type === 'chunk');
  const has = (module) => !module.startsWith('\0') && moduleKey(module) === key;

  return (
    chunks.find((chunk) => chunk.facadeModuleId && has(chunk.facadeModuleId)) ??
    chunks.find((chunk) => chunk.moduleIds?.some(has))
  );
}

// The asset of the bundle whose source file is the module id, without its
// query. Vite gives an asset its source path relative to root.
function assetOfModule(bundle, id, root) {
  const file = path.resolve(id.split('?')[0]);
  return Object.values(bundle).find(
    (out) =>
      out.type === 'asset' &&
      (out.originalFileNames ?? []).some(
        (name) => path.resolve(root, name) === file,
      ),
  );
}

// The files, in order, that the chunk named start loads before it runs: the
// chunks it imports statically, in turn, and their styles. With assets, also
// the assets that each chunk names. Vite emits a ?worker script as an asset
// that no chunk lists, so a chunk that names it in its code loads it.
function filesOf(bundle, start, { assets }) {
  const files = [];
  const scripts = Object.values(bundle)
    .filter((out) => out.type === 'asset' && out.fileName.endsWith('.js'))
    .map((out) => out.fileName);
  const pending = [start];
  const visited = new Set();

  while (pending.length > 0) {
    const name = pending.shift();
    const chunk = bundle[name];
    if (visited.has(name) || chunk?.type !== 'chunk') {
      continue;
    }
    visited.add(name);

    files.push(chunk.fileName, ...(chunk.viteMetadata?.importedCss ?? []));
    if (assets) {
      files.push(...(chunk.viteMetadata?.importedAssets ?? []));
      files.push(...scripts.filter((file) => chunk.code.includes(file)));
    }
    pending.push(...chunk.imports);
  }

  return files;
}

// The URLs, in order and each one time, of the files that Builder loads
// first: those of the chunk of the view at path view, and those of each
// module in modules, which Builder imports on first use. A file that html
// already loads is not in the list. base is Vite's resolved base, and root
// its root. Throws when the bundle has no file for the view or a module.
export function builderFileList(
  bundle,
  { view, modules = [] },
  { html = '', base = '/', root = '/' } = {},
) {
  const viewChunk = viewChunkOf(bundle, view);
  if (!viewChunk) {
    throw new Error(
      `The build has no chunk for ${view}, so index.html cannot list Builder's files.`,
    );
  }

  const files = filesOf(bundle, viewChunk.fileName, { assets: false });
  for (const id of modules) {
    const chunk = chunkOfModule(bundle, id);
    const asset = chunk ? null : assetOfModule(bundle, id, root);
    if (!chunk && !asset) {
      throw new Error(
        `The build has no file for ${id}, so index.html cannot list Builder's files.`,
      );
    }
    if (asset) {
      files.push(asset.fileName);
      continue;
    }

    const found = filesOf(bundle, chunk.fileName, { assets: true });
    // A ?url or ?worker module is a short chunk that names the file to load.
    const named = found.some(
      (file) => bundle[file]?.type === 'asset' && !file.endsWith('.css'),
    );
    if (/\?(?:url|worker)\b/.test(id) && !named) {
      throw new Error(
        `The build has no file that ${id} loads, so index.html cannot list Builder's files.`,
      );
    }
    files.push(...found);
  }

  const loaded = loadedUrls(html);
  return [
    ...new Set(files.map((file) => base + file).filter((u) => !loaded.has(u))),
  ];
}

// The tag, as a Vite HtmlTagDescriptor, that holds the list of urls as JSON
// data. A browser does not run a script element of this type.
export function fileListTag(urls) {
  return {
    tag: 'script',
    attrs: { type: 'application/json', id: FILE_LIST_ID },
    // "<" as an escape, so the data cannot end the element
    children: JSON.stringify(urls).replaceAll('<', '\\u003c'),
    injectTo: 'head',
  };
}

// The plugin. view is the path of Builder's view, src/views/Builder.vue, and
// modules are the ids of the modules that Builder imports on first use.
export default function builderPrefetch({ view, modules = [] }) {
  let base = '/';
  let root = '/';

  return {
    name: 'phenix:builder-prefetch',
    apply: 'build',
    configResolved(config) {
      base = config.base;
      root = config.root;
    },
    transformIndexHtml: {
      // After Vite adds its own script, modulepreload and stylesheet tags.
      order: 'post',
      handler(html, { bundle }) {
        const urls = builderFileList(
          bundle ?? {},
          { view, modules },
          { html, base, root },
        );

        return [fileListTag(urls)];
      },
    },
  };
}
