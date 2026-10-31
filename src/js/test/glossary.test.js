import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from 'vitest';

import { compareTerms, glossary } from '@/utils/glossary.js';

// The canonical expansion of each abbreviation. The glossary and every
// <abbr title> in the views must use it.
const abbreviations = {
  CC: 'command and control',
  CPU: 'central processing unit',
  DNB: 'do not boot',
  GiB: 'gibibytes',
  HIL: 'hardware in the loop',
  IP: 'Internet Protocol',
  ISO: 'ISO 9660 optical disc image',
  JWT: 'JSON Web Token',
  MiB: 'mebibytes',
  NIC: 'network interface card',
  RBAC: 'role-based access control',
  RDP: 'Remote Desktop Protocol',
  SoH: 'state of health',
  VLAN: 'virtual local area network',
  VM: 'virtual machine',
  VNC: 'Virtual Network Computing',
  YAML: "YAML Ain't Markup Language",
};

test('the glossary is in alphabetical order', () => {
  const terms = glossary.map((e) => e.term);
  expect(terms).toEqual([...terms].sort(compareTerms));
});

test('each term has a unique id that a URL fragment can use', () => {
  const ids = glossary.map((e) => e.id);
  expect(new Set(ids).size).toBe(ids.length);
  for (const id of ids) {
    expect(id).toMatch(/^[a-z0-9]+(-[a-z0-9]+)*$/);
  }
});

test('no term id is also the id of an element in the app', () => {
  const src = join(import.meta.dirname, '..');
  const files = [
    'index.html',
    ...readdirSync(join(src, 'src'), { recursive: true })
      .filter((f) => f.endsWith('.vue'))
      .map((f) => join('src', f)),
  ];
  const ids = new Set();
  for (const file of files) {
    const text = readFileSync(join(src, file), 'utf8');
    for (const [, id] of text.matchAll(/(?<![:\w-])id="([^"]+)"/g)) {
      ids.add(id);
    }
  }
  expect(ids.has('app')).toBe(true);
  for (const { id } of glossary) {
    expect(ids.has(id), id).toBe(false);
  }
});

test('each term has a short definition', () => {
  for (const { term, definition } of glossary) {
    expect(definition, term).toMatch(/\.$/);
    expect(definition.split(/\.\s/).length, term).toBeLessThanOrEqual(2);
  }
});

test('the glossary expands each abbreviation as the UI does', () => {
  for (const [term, expansion] of Object.entries(abbreviations)) {
    const entry = glossary.find((e) => e.term === term);
    expect(entry?.expansion, term).toBe(expansion);
  }
});

// An abbreviation, its plural with "s", or the SOH spelling of SoH.
function expansionOf(text) {
  if (text === 'SOH') return abbreviations.SoH;
  if (abbreviations[text]) return abbreviations[text];
  const singular = text.replace(/s$/, '');
  if (singular !== text && abbreviations[singular]) {
    return `${abbreviations[singular]}s`;
  }
  return undefined;
}

test('each <abbr> in the views has the canonical expansion', () => {
  const src = join(import.meta.dirname, '../src');
  const files = readdirSync(src, { recursive: true }).filter((f) =>
    f.endsWith('.vue'),
  );
  const abbr = /<abbr\s[^>]*?(?<![:\w-])title="([^"]*)"[^>]*>([^<]*)<\/abbr>/g;
  let count = 0;

  for (const file of files) {
    const text = readFileSync(join(src, file), 'utf8');
    for (const [, title, inner] of text.matchAll(abbr)) {
      const short = inner.trim();
      expect(expansionOf(short), `${file}: ${short}`).toBeDefined();
      expect(title, `${file}: ${short}`).toBe(expansionOf(short));
      count += 1;
    }
  }
  expect(count).toBeGreaterThan(0);
});

test('the glossary defines the domain terms that the UI shows', () => {
  const terms = glossary.map((e) => e.term.toLowerCase());
  for (const term of [
    'app',
    'backing image',
    'cc active',
    'disk',
    'experiment',
    'host',
    'miniccc',
    'minimega',
    'scenario',
    'scorch',
    'snapshot',
    'state of health',
    'tap',
    'topology',
    'tunneler',
    'vlan alias',
  ]) {
    expect(terms).toContain(term);
  }
});
