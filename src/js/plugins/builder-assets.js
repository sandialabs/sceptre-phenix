// Builder's files, the ones only its page loads, compressed for the server
// (src/go/web/builder_assets.go), which sends them compressed to clients
// that accept it and cached for good.
//
// The plugin writes each file's Brotli and gzip copies next to it, as
// <file>.br and <file>.gz, when they are smaller, and lists the files in
// builder-assets.json, beside index.html.
import { Buffer } from 'node:buffer';
import path from 'node:path';
import { brotliCompressSync, constants, gzipSync } from 'node:zlib';

export const BUILDER_ASSET_LIST = 'builder-assets.json';

// The copies the server can send, by the suffix of their file.
export const ENCODINGS = {
  '.br': (content) =>
    brotliCompressSync(content, {
      params: {
        [constants.BROTLI_PARAM_MODE]: constants.BROTLI_MODE_TEXT,
        [constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY,
        [constants.BROTLI_PARAM_SIZE_HINT]: content.length,
      },
    }),
  '.gz': (content) =>
    gzipSync(content, { level: constants.Z_BEST_COMPRESSION }),
};

// The files, by name, that the given chunks load, statically or lazily, with
// their styles and assets, without passing through the chunk named skip.
// A Web Worker imported with ?worker is a script Vite bundles apart and
// emits as an asset no chunk lists: the chunk that starts it names its file
// in its code, which is how it is found.
function filesFrom(bundle, from, skip) {
  const files = new Set();
  const seen = new Set([skip]);
  const pending = [...from];
  const scripts = Object.values(bundle)
    .filter((out) => out.type === 'asset' && out.fileName.endsWith('.js'))
    .map((out) => out.fileName);

  while (pending.length > 0) {
    const name = pending.pop();
    if (seen.has(name)) {
      continue;
    }
    seen.add(name);

    const chunk = bundle[name];
    if (chunk?.type !== 'chunk') {
      continue;
    }

    files.add(chunk.fileName);
    for (const file of chunk.viteMetadata?.importedCss ?? []) {
      files.add(file);
    }
    for (const file of chunk.viteMetadata?.importedAssets ?? []) {
      files.add(file);
    }
    for (const file of scripts) {
      if (!files.has(file) && chunk.code.includes(file)) {
        files.add(file);
      }
    }

    pending.push(...chunk.imports, ...chunk.dynamicImports);
  }

  return files;
}

// The files, sorted, that only Builder loads: those its view's chunk
// loads, less those the rest of the UI can load without it. null when the
// bundle has no chunk for the view.
export function builderFiles(bundle, view) {
  const chunks = Object.values(bundle).filter((out) => out.type === 'chunk');
  const viewChunk = chunks.find(
    (chunk) =>
      chunk.facadeModuleId &&
      path.resolve(chunk.facadeModuleId) === path.resolve(view),
  );
  if (!viewChunk) {
    return null;
  }

  const entries = chunks
    .filter((chunk) => chunk.isEntry)
    .map((chunk) => chunk.fileName);
  const shared = filesFrom(bundle, entries, viewChunk.fileName);

  return [...filesFrom(bundle, [viewChunk.fileName], null)]
    .filter((file) => !shared.has(file))
    .sort();
}

// The plugin. view is the path of Builder's view, src/views/Builder.vue.
export default function builderAssets({ view }) {
  return {
    name: 'phenix:builder-assets',
    apply: 'build',
    generateBundle: {
      // After Vite's own hooks, which still change the chunks' code.
      order: 'post',
      handler(_, bundle) {
        const files = builderFiles(bundle, view);
        if (!files) {
          this.error(
            `The build has no chunk for ${view}, so the server would send Builder's files uncompressed.`,
          );
        }

        for (const file of files) {
          const out = bundle[file];
          const content = Buffer.from(
            out.type === 'chunk' ? out.code : out.source,
          );

          for (const [suffix, compress] of Object.entries(ENCODINGS)) {
            const compressed = compress(content);
            if (compressed.length < content.length) {
              this.emitFile({
                type: 'asset',
                fileName: file + suffix,
                source: compressed,
              });
            }
          }
        }

        this.emitFile({
          type: 'asset',
          fileName: BUILDER_ASSET_LIST,
          source: `${JSON.stringify(files, null, 2)}\n`,
        });
      },
    },
  };
}
