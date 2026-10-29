// The app's compiled styles, for files run with `@vitest-environment node`:
// happy-dom lays nothing out, so tests read the rules themselves.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import * as sass from 'sass';

const ROOT = join(import.meta.dirname, '..', '..');

let compiled;

// main.scss as the browser gets it, compiled once per test file
function appCss() {
  compiled ??= sass.compile(join(ROOT, 'src/assets/main.scss'), {
    loadPaths: [join(ROOT, 'node_modules')],
    quietDeps: true,
    logger: sass.Logger.silent,
  }).css;
  return compiled;
}

// each style rule: its selectors, the @media and @container conditions
// around it, its body
function rules(css) {
  const found = [];
  const open = [];
  let text = '';
  for (const ch of css.replace(/\/\*[\s\S]*?\*\//g, '')) {
    if (ch === '{') {
      open.push(text.trim());
      text = '';
    } else if (ch === '}') {
      const prelude = open.pop();
      if (!prelude.startsWith('@')) {
        found.push({
          selectors: prelude.split(',').map((s) => s.trim()),
          media: open.filter((p) => /^@(media|container)\b/.test(p)),
          body: text,
        });
      }
      text = '';
    } else {
      text += ch;
    }
  }
  return found;
}

// the declarations `css` gives `selector` alone, the last of each winning
function pick(css, selector, media) {
  const found = {};
  for (const rule of rules(css)) {
    if (!rule.selectors.includes(selector)) continue;
    const inMedia = media
      ? rule.media.some((m) => m.includes(media))
      : rule.media.length === 0;
    if (!inMedia) continue;
    for (const decl of rule.body.split(';')) {
      const [name, ...value] = decl.split(':');
      if (value.length) found[name.trim()] = value.join(':').trim();
    }
  }
  return found;
}

// The declarations the app's styles give `selector` alone, the last of each
// winning: those inside an @media or @container whose condition contains
// `media`, or, with no `media`, those outside both.
export function declarations(selector, media = null) {
  return pick(appCss(), selector, media);
}

// The same, from the plain CSS <style> blocks of the component at `file`
// (relative to src/js), with selectors as its source writes them.
export function componentDeclarations(file, selector, media = null) {
  const source = readFileSync(join(ROOT, file), 'utf8');
  const css = [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)]
    .map((m) => m[1])
    .join('\n');
  return pick(css, selector, media);
}
