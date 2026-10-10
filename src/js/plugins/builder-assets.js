// Builder's files, the ones only its page loads, compressed for the server
// (src/go/web/builder_assets.go), which sends them compressed to clients
// that accept it and cached for good.
//
// The plugin writes each file's Brotli and gzip copies next to it, as
// <file>.br and <file>.gz, when they are smaller, and lists the files in
// builder-assets.json, beside index.html.
import { Buffer } from 'node:buffer';
import path from 'node:path';
import process from 'node:process';
import { brotliCompressSync, constants, gzipSync } from 'node:zlib';

export const BUILDER_ASSET_LIST = 'builder-assets.json';

// The environment variable that sets the Brotli quality, 0 to 11.
export const BROTLI_QUALITY_VARIABLE = 'PHENIX_BROTLI_QUALITY';

// The Brotli quality without PHENIX_BROTLI_QUALITY. Measured on Builder's
// files (7 files, 3.1 MB), quality 9 takes 74 ms and writes 786 kB of
// copies, 10 takes 1.3 s for 725 kB, and 11, the most, 3.0 s for 711 kB.
// The server sends the files with a one-year immutable cache, so a browser
// downloads the 74 kB that 11 saves once per release: local and CI builds
// use 9, and the Docker, Podman and Debian package builds set 11.
export const DEFAULT_BROTLI_QUALITY = 9;

// The Brotli quality PHENIX_BROTLI_QUALITY in env names, a whole number from
// 0 to 11, or DEFAULT_BROTLI_QUALITY when it is not set; null for any other
// value.
export function brotliQuality(env) {
  const value = env[BROTLI_QUALITY_VARIABLE];
  if (value === undefined) {
    return DEFAULT_BROTLI_QUALITY;
  }

  return /^(?:\d|1[01])$/.test(value) ? Number(value) : null;
}

// The copies the server can send, by the suffix of their file, with Brotli
// at the given quality.
export function encodings(quality) {
  return {
    '.br': (content) =>
      brotliCompressSync(content, {
        params: {
          [constants.BROTLI_PARAM_MODE]: constants.BROTLI_MODE_TEXT,
          [constants.BROTLI_PARAM_QUALITY]: quality,
          [constants.BROTLI_PARAM_SIZE_HINT]: content.length,
        },
      }),
    '.gz': (content) =>
      gzipSync(content, { level: constants.Z_BEST_COMPRESSION }),
  };
}

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

// The plugin. view is the path of Builder's view, src/views/Builder.vue;
// env holds PHENIX_BROTLI_QUALITY, and is the process's environment unless
// given.
export default function builderAssets({ view, env = process.env }) {
  let quality = DEFAULT_BROTLI_QUALITY;

  return {
    name: 'phenix:builder-assets',
    apply: 'build',
    // Refuses a quality it cannot use before anything is built.
    buildStart() {
      quality = brotliQuality(env);
      if (quality === null) {
        this.error(
          `${BROTLI_QUALITY_VARIABLE} must be a whole number from 0 to 11, not "${env[BROTLI_QUALITY_VARIABLE]}".`,
        );
      }
    },
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

        const copies = Object.entries(encodings(quality));
        for (const file of files) {
          const out = bundle[file];
          const content = Buffer.from(
            out.type === 'chunk' ? out.code : out.source,
          );

          for (const [suffix, compress] of copies) {
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
