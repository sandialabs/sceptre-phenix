// Builder packages: decoding one as strictly as the server does, what the
// server says the diagram needs, which needs the user may have created, and
// the list the Upload dialog shows (rendered on the server).

import { describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import PackageImport from '@/components/builder/dialogs/PackageImport.vue';
import {
  PACKAGE_SCHEMA_URI,
  PackageError,
  canCreate,
  createTickedConfigs,
  decodePackage,
  groupDependencies,
  isPackageValue,
  packageFileName,
  packagedConfig,
  readDependencies,
  tickedDependencies,
} from '@/builder/package.js';

import { sampleDocument } from './fixtures.js';

function scenarioConfig(name) {
  return {
    apiVersion: 'phenix.sandia.gov/v2',
    kind: 'Scenario',
    metadata: { name, annotations: { topology: 'lab' } },
    spec: { apps: [{ name: 'ntp' }] },
  };
}

// A package of the sample document carrying one Scenario config.
function samplePackage(document = sampleDocument().doc) {
  return {
    $schema: PACKAGE_SCHEMA_URI,
    document,
    scenarios: { 'pkg-scenario': scenarioConfig('pkg-scenario') },
    requirements: {
      scenarios: ['pkg-scenario', 'pkg-other'],
      topologies: [],
      templates: [],
      icons: [],
      images: [{ name: 'ubuntu.qc2', usedBy: ['alpha'] }],
      apps: ['ntp'],
      files: [],
    },
  };
}

function dependency(kind, name, status, packaged = false, detail = '') {
  return { kind, name, status, packaged, detail };
}

// count entries of a list: prefix0, prefix1 and so on.
function entries(prefix, count) {
  return Array.from({ length: count }, (_, index) => `${prefix}${index}`);
}

// Makes a package value carry count configs of one list (scenarios or
// topologies), named pkg-0, pkg-1 and so on, and its requirements name
// them.
function carry(value, key, count) {
  const kind = key === 'topologies' ? 'Topology' : 'Scenario';
  const names = entries('pkg-', count);
  const config = (name) => ({
    apiVersion: 'phenix.sandia.gov/v1',
    kind,
    metadata: { name },
    spec: {},
  });

  value[key] = Object.fromEntries(names.map((name) => [name, config(name)]));
  value.requirements[key] = names;
}

describe('decodePackage', () => {
  test('decodes a package, the document as an uploaded document', () => {
    const document = sampleDocument().doc;
    const pkg = decodePackage(samplePackage(document));

    expect(pkg.document.metadata.id).toBe(document.metadata.id);
    expect(pkg.scenarios['pkg-scenario'].kind).toBe('Scenario');
    expect(pkg.topologies).toEqual({});
    expect(pkg.requirements.images).toEqual([
      { name: 'ubuntu.qc2', usedBy: ['alpha'] },
    ]);
  });

  test('tells a package from a document by its schema', () => {
    expect(isPackageValue(samplePackage())).toBe(true);
    expect(isPackageValue(sampleDocument().doc)).toBe(false);
    expect(isPackageValue(null)).toBe(false);
    expect(isPackageValue([samplePackage()])).toBe(false);
  });

  test.each([
    [
      'another schema',
      (value) => {
        value.$schema = 'https://phenix.sandia.gov/schemas/builder/v1';
      },
      'A Builder package names the schema',
    ],
    [
      'an unknown field',
      (value) => {
        value.extra = true;
      },
      'package: unknown field "extra"',
    ],
    [
      'an unknown requirement',
      (value) => {
        value.requirements.extra = [];
      },
      'requirements: unknown field "extra"',
    ],
    [
      'a missing list',
      (value) => {
        delete value.requirements.files;
      },
      'requirements.files: the list is required',
    ],
    [
      'images that are no list',
      (value) => {
        value.requirements.images = {};
      },
      'requirements.images: the list is required',
    ],
    [
      'an unknown field of an image',
      (value) => {
        value.requirements.images[0].size = 1;
      },
      'requirements.images[0]: unknown field "size"',
    ],
    [
      'a blank entry',
      (value) => {
        value.requirements.apps = [' '];
      },
      'requirements.apps[0]: must not be blank',
    ],
    [
      'an entry that is no text',
      (value) => {
        value.requirements.apps = [7];
      },
      'requirements.apps[0]: must be a name or a path',
    ],
    [
      'a list longer than a package lists',
      (value) => {
        value.requirements.files = entries('/phenix/f', 1001);
      },
      'requirements.files: the list holds at most 1000 entries, not 1001',
    ],
    [
      'more disk images than a package lists',
      (value) => {
        value.requirements.images = entries('disk-', 1001).map((name) => ({
          name,
          usedBy: [],
        }));
      },
      'requirements.images: the list holds at most 1000 entries, not 1001',
    ],
    [
      'an entry longer than a package lists, in bytes',
      (value) => {
        // 2049 characters of two bytes each.
        value.requirements.apps = ['é'.repeat(2049)];
      },
      'requirements.apps[0]: must be at most 4096 bytes',
    ],
    [
      'an entry with a control character',
      (value) => {
        value.requirements.icons = ['a\tb'];
      },
      'requirements.icons[0]: must not contain control characters',
    ],
    [
      'an image name with a control character',
      (value) => {
        value.requirements.images[0].name = 'ubuntu\n.qc2';
      },
      'requirements.images[0].name: must not contain control characters',
    ],
    [
      'a host of an image with a control character',
      (value) => {
        value.requirements.images[0].usedBy = ['alpha\u007f'];
      },
      'requirements.images[0].usedBy[0]: must not contain control characters',
    ],
    [
      'a config name longer than a config name may be',
      (value) => {
        const name = 'a'.repeat(257);
        const config = scenarioConfig(name);

        value.scenarios = { [name]: config };
        value.requirements.scenarios = [name];
      },
      `scenarios.${'a'.repeat(257)}: "${'a'.repeat(64)}..." is not a config name`,
    ],
    [
      'more Scenario configs than a document names',
      (value) => carry(value, 'scenarios', 21),
      'scenarios: a package carries at most 20 Scenario configs, not 21',
    ],
    [
      'more Topology configs than a package carries',
      (value) => carry(value, 'topologies', 101),
      'topologies: a package carries at most 100 Topology configs, not 101',
    ],
    [
      'a Builder annotation on a config',
      (value) => {
        value.scenarios['pkg-scenario'].metadata.annotations = {
          topology: 'lab',
          'builder-xml': '<mxGraphModel/>',
          'builder-doc': '{"id":"forged"}',
        };
      },
      'scenarios.pkg-scenario.metadata.annotations: "builder-doc" is a Builder annotation, which a package never carries',
    ],
    [
      'an unknown field of a config',
      (value) => {
        value.scenarios['pkg-scenario'].status = {};
      },
      'scenarios.pkg-scenario: unknown field "status"',
    ],
    [
      'a config of another kind',
      (value) => {
        value.scenarios['pkg-scenario'].kind = 'Topology';
      },
      'scenarios.pkg-scenario.kind: must be "Scenario"',
    ],
    [
      'a config named apart from its key',
      (value) => {
        value.scenarios['pkg-scenario'].metadata.name = 'other';
      },
      'scenarios.pkg-scenario.metadata.name: must be "pkg-scenario"',
    ],
    [
      'an apiVersion of no phenix config',
      (value) => {
        value.scenarios['pkg-scenario'].apiVersion = 'v2';
      },
      'scenarios.pkg-scenario.apiVersion: must be phenix.sandia.gov/v<number>',
    ],
    [
      'a config without a spec',
      (value) => {
        delete value.scenarios['pkg-scenario'].spec;
      },
      'scenarios.pkg-scenario.spec: a config needs a spec',
    ],
    [
      'a carried config the requirements do not name',
      (value) => {
        value.requirements.scenarios = ['pkg-other'];
      },
      'scenarios.pkg-scenario: the package carries it, but its requirements do not name it',
    ],
    [
      'a document that is not valid',
      (value) => {
        value.document.extra = true;
      },
      'document: ',
    ],
  ])('refuses %s', (_, change, message) => {
    const value = samplePackage();

    change(value);

    expect(() => decodePackage(value)).toThrow(PackageError);
    expect(() => decodePackage(value)).toThrow(message);
  });
});

describe('what the diagram needs', () => {
  test('reads the dependencies the server lists, leaving out what it does not know', () => {
    expect(
      readDependencies({
        dependencies: [
          { kind: 'scenario', name: 'a', status: 'missing', packaged: true },
          { kind: 'icon', name: 'b', status: 'present', detail: 'Fine.' },
          { kind: 'disk', name: 'c', status: 'present' },
          { kind: 'app', name: 'd', status: 'gone' },
          { kind: 'file', status: 'unknown' },
        ],
      }),
    ).toEqual([
      dependency('scenario', 'a', 'missing', true),
      dependency('icon', 'b', 'present', false, 'Fine.'),
    ]);
    expect(readDependencies({})).toEqual([]);
  });

  test('groups them by kind, in the order the server lists the kinds', () => {
    const groups = groupDependencies([
      dependency('file', '/etc/x', 'unknown'),
      dependency('scenario', 'a', 'present'),
      dependency('image', 'ubuntu.qc2', 'missing'),
      dependency('scenario', 'b', 'missing', true),
    ]);

    expect(groups.map((group) => [group.label, group.items.length])).toEqual([
      ['Scenario configs', 2],
      ['Disk images', 1],
      ['Files', 1],
    ]);
  });

  test('offers to create only a config the package carries and the server lacks', () => {
    expect(canCreate(dependency('scenario', 'a', 'missing', true))).toBe(true);
    expect(canCreate(dependency('topology', 'a', 'missing', true))).toBe(true);
    expect(canCreate(dependency('scenario', 'a', 'missing', false))).toBe(
      false,
    );
    expect(canCreate(dependency('scenario', 'a', 'different', true))).toBe(
      false,
    );
    expect(canCreate(dependency('scenario', 'a', 'present', true))).toBe(false);
    expect(canCreate(dependency('icon', 'a', 'missing', true))).toBe(false);
    expect(canCreate(dependency('image', 'a', 'missing', true))).toBe(false);
  });

  test('hands on only the configs ticked that may be created', () => {
    const dependencies = [
      dependency('scenario', 'a', 'missing', true),
      dependency('scenario', 'b', 'missing', true),
      dependency('scenario', 'c', 'different', true),
    ];

    expect(
      tickedDependencies(dependencies, ['scenario/b', 'scenario/c']),
    ).toEqual([dependencies[1]]);
    expect(tickedDependencies(dependencies, [])).toEqual([]);
  });

  test('finds the config a dependency names in the package', () => {
    const pkg = decodePackage(samplePackage());

    expect(
      packagedConfig(pkg, dependency('scenario', 'pkg-scenario', 'missing')),
    ).toEqual(scenarioConfig('pkg-scenario'));
    expect(
      packagedConfig(pkg, dependency('topology', 'pkg-scenario', 'missing')),
    ).toBeNull();
    expect(
      packagedConfig(pkg, dependency('scenario', 'pkg-other', 'missing')),
    ).toBeNull();
  });

  test('creates the ticked configs one at a time, and goes on after a failure', async () => {
    const pkg = {
      scenarios: { a: scenarioConfig('a'), b: scenarioConfig('b') },
      topologies: {},
    };
    const refused = new Error('config already exists');
    const created = [];
    const create = vi.fn(async (config) => {
      if (config.metadata.name === 'a') {
        throw refused;
      }

      created.push(config.metadata.name);
    });

    const result = await createTickedConfigs(
      pkg,
      [
        dependency('scenario', 'a', 'missing', true),
        dependency('scenario', 'b', 'missing', true),
        dependency('scenario', 'c', 'present', true),
        dependency('image', 'ubuntu.qc2', 'missing'),
      ],
      create,
    );

    const asked = create.mock.calls.map(([config]) => config.metadata.name);

    expect(asked).toEqual(['a', 'b']);
    expect(created).toEqual(['b']);
    expect(result.created.map((entry) => entry.name)).toEqual(['b']);
    expect(result.failed).toEqual([
      {
        dependency: dependency('scenario', 'a', 'missing', true),
        error: refused,
      },
    ]);
  });

  test('names a package after its diagram', () => {
    const doc = { metadata: { name: 'Pump station' } };

    expect(packageFileName(doc, 'yaml')).toBe('pump-station.package.yaml');
    expect(packageFileName(doc, 'json')).toBe('pump-station.package.json');
  });
});

describe('the package list of the Upload dialog', () => {
  async function render(dependencies, props = {}) {
    const app = createSSRApp({
      render: () => h(PackageImport, { dependencies, ...props }),
    });

    return renderToString(app);
  }

  // The opening tag of the button of a test id.
  function button(html, testId) {
    return html.match(
      new RegExp(`<button\\b[^>]*data-testid="${testId}"[^>]*>`),
    )[0];
  }

  test('while the configs are created, Cancel is unavailable and says what the dialog is doing', async () => {
    const dependencies = [dependency('scenario', 'pkg-new', 'missing', true)];
    const busy = await render(dependencies, {
      busy: true,
      progress: 'Creating 1 config on this server…',
    });
    const cancel = button(busy, 'upload-package-cancel');

    expect(cancel).toContain('aria-disabled="true"');
    expect(cancel).toContain('aria-describedby="upload-package-progress"');
    expect(button(busy, 'upload-package-continue')).toContain(
      'aria-busy="true"',
    );
    expect(busy).toMatch(
      /<p id="upload-package-progress"[^>]*>\s*Creating 1 config on this server…\s*<\/p>/,
    );

    const idle = await render(dependencies);

    expect(button(idle, 'upload-package-cancel')).not.toMatch(
      /aria-disabled|aria-describedby/,
    );
    expect(idle).not.toContain('upload-package-progress');
  });

  // What an element holds, as text: tags out, white space as one space.
  function textOf(html) {
    return html
      .replace(/<[^>]*>/g, '')
      .replace(/\s+/g, ' ')
      .trim();
  }

  // The text of the first group of each match of pattern in html.
  function texts(html, pattern) {
    return [...html.matchAll(pattern)].map((match) => textOf(match[1]));
  }

  test('lists each need under its kind with its status in words, and a checkbox only for a config to create', async () => {
    const html = await render([
      dependency('scenario', 'pkg-new', 'missing', true),
      dependency('scenario', 'pkg-listed', 'missing', false),
      dependency('scenario', 'pkg-diff', 'different', true, 'Another spec.'),
      dependency('topology', 'pkg-inc', 'missing', true),
      dependency('topology', 'pkg-shared', 'present', true),
      dependency('image', 'ubuntu.qc2', 'missing', false, 'Used by alpha.'),
      dependency('app', 'ntp', 'unknown', false, 'Your role cannot list apps.'),
      dependency('file', '/etc/x', 'unknown'),
    ]);

    expect(texts(html, /<h3\b[^>]*>([\s\S]*?)<\/h3>/g)).toEqual([
      'Scenario configs',
      'Included topologies',
      'Disk images',
      'Apps',
      'Files',
    ]);

    const boxes = [
      ...html.matchAll(/<input\b[^>]*data-testid="([^"]+)"[^>]*>/g),
    ].map(([input, testId]) => ({
      testId,
      checked: /\schecked\b/.test(input),
    }));

    expect(boxes).toEqual([
      { testId: 'upload-package-create-scenario-pkg-new', checked: false },
      { testId: 'upload-package-create-topology-pkg-inc', checked: false },
    ]);

    // The text of the list item of a test id.
    const item = (testId) => {
      const start = html.indexOf(`data-testid="${testId}"`);
      const end = html.indexOf('</li>', start);

      return textOf(html.slice(html.indexOf('>', start) + 1, end));
    };

    // Each part a sentence of its own to a screen reader, through the
    // separators only it reads.
    expect(item('upload-package-item-scenario-pkg-new')).toBe(
      'pkg-new: Missing, in the package Create on this server',
    );
    expect(item('upload-package-item-scenario-pkg-diff')).toBe(
      'pkg-diff: Different, in the package. Another spec.',
    );
    expect(item('upload-package-item-image-ubuntu.qc2')).toBe(
      'ubuntu.qc2: Missing. Used by alpha.',
    );
    expect(item('upload-package-item-file-/etc/x')).toBe('/etc/x: Not checked');
    expect(textOf(html)).toMatch(
      /^The diagram needs 8 items, 4 of them missing on this server\. Tick each config the package carries that this server is to have\. Nothing on the server is replaced\./,
    );
  });

  test('says so when the package lists nothing', async () => {
    const html = await render([]);

    expect(textOf(html)).toContain(
      'The package lists nothing the diagram needs.',
    );
    expect(html).not.toMatch(/type="checkbox"/);
  });
});
