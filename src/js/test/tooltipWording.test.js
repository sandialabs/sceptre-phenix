// @vitest-environment node
// Static Buefy tooltip labels and scripts' tooltip strings spell out what they name: no lowercase
// soh, scorch, vm, vlan, vnc or cpu; bound labels and title attributes are not checked.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { expect, it } from 'vitest';

const src = fileURLToPath(new URL('../src', import.meta.url));
const ABBREVIATION = /\b(soh|scorch|vms?|vlans?|vnc|cpus?)\b/;
const SIDE = /\bis-(top|left|right|bottom)\b/;
// a <b-tooltip> start tag; a quoted value can hold '>', as in v-if="loop > 0"
const TOOLTIP_TAG = /<b-tooltip\b((?:[^>"']|"[^"]*"|'[^']*')*)>/g;

const sourceFiles = (dir) =>
  fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) return sourceFiles(file);
    return /\.(vue|js)$/.test(entry.name) ? [file] : [];
  });

// each static tooltip label in a file, and the string tooltips scripts give
function tooltipLabels(file) {
  const text = fs.readFileSync(file, 'utf8');
  const labels = [];
  for (const [, attrs] of text.matchAll(TOOLTIP_TAG)) {
    const label = /(?:^|\s)label="([^"]*)"/.exec(attrs);
    if (label) labels.push(label[1]);
  }
  for (const [, tooltip] of text.matchAll(/\btooltip: '([^']*)'/g)) {
    labels.push(tooltip);
  }
  return labels;
}

it('finds the tooltips it checks', () => {
  const labels = sourceFiles(src).flatMap(tooltipLabels);
  expect(labels).toContain('view state of health');
  expect(labels).toContain('create VM snapshot');
});

it('gives no tooltip a lowercase abbreviation', () => {
  const offenders = sourceFiles(src).flatMap((file) =>
    tooltipLabels(file)
      .filter((label) => ABBREVIATION.test(label))
      .map((label) => `${path.relative(src, file)}: ${label}`),
  );
  expect(offenders).toEqual([]);
});

// each <b-tooltip> whose type, static or bound, names a side
const sidesInType = (text) =>
  [...text.matchAll(TOOLTIP_TAG)]
    .map(([, attrs]) => /(?:^|\s):?type="([^"]*)"/.exec(attrs)?.[1])
    .filter((type) => type && SIDE.test(type));

// Main bug: a tooltip side set in type opened off screen.
it('sets no tooltip side in type, which Buefy reads as the color only', () => {
  expect(
    sidesInType('<b-tooltip v-if="n > 0"\n  type="is-light is-left">'),
  ).toEqual(['is-light is-left']);
  const offenders = sourceFiles(src)
    .filter((file) => file.endsWith('.vue'))
    .flatMap((file) =>
      sidesInType(fs.readFileSync(file, 'utf8')).map(
        (type) => `${path.relative(src, file)}: ${type}`,
      ),
    );
  expect(offenders).toEqual([]);
});
