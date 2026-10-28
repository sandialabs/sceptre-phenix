// The app's compiled styles, for files run with `@vitest-environment node`:
// happy-dom lays nothing out, so tests read the rules themselves.
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

// each style rule: its selectors, the @media conditions around it, its body
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
          media: open.filter((p) => p.startsWith('@media')),
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

// The declarations the app's styles give `selector` alone, the last of each
// winning: those inside an @media whose condition contains `media`, or, with
// no `media`, those outside any @media.
export function declarations(selector, media = null) {
  const found = {};
  for (const rule of rules(appCss())) {
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
