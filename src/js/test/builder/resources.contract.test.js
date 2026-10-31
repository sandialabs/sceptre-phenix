// The vCPU and memory totals of the editor header (resources.js).

import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import bundle from '@/builder/schema/builder-v1.schema.json';
import {
  DEFAULT_MEMORY_MB,
  DEFAULT_VCPUS,
  deviceResources,
  diagramResources,
  driveImages,
  formatMemory,
  resourceTotals,
  resourcesParts,
  resourcesText,
  selectionResources,
  specWhole,
  startsDevice,
} from '@/builder/resources.js';

function device(id, hardware, extra = {}) {
  return {
    id,
    kind: 'device',
    device: {
      hostname: id,
      spec: { general: { hostname: id }, hardware, ...extra.spec },
      ...extra.device,
    },
    ...extra.node,
  };
}

const NUMBER_RUNS = { numRuns: 200 };

// A device that sets its vCPUs and memory, or leaves either out.
const deviceArb = fc
  .record({
    vcpus: fc.option(fc.integer({ min: 1, max: 64 }), { nil: undefined }),
    memory: fc.option(fc.integer({ min: 1, max: 65536 }), { nil: undefined }),
    external: fc.boolean(),
  })
  .map(({ vcpus, memory, external }) =>
    device(
      'x',
      {
        os_type: 'linux',
        ...(vcpus === undefined ? {} : { vcpus }),
        ...(memory === undefined ? {} : { memory }),
      },
      external ? { spec: { external: true } } : {},
    ),
  );

const devicesArb = fc
  .array(deviceArb, { maxLength: 30 })
  .map((nodes) => nodes.map((node, index) => ({ ...node, id: `d${index}` })));

describe('the defaults', () => {
  // The phenix schema's defaults, which the server's preflight uses too
  // (TestPreflightDefaultsMatchSchema in api/builder).
  test('equal those of the phenix node schema', () => {
    const hardware =
      bundle.$defs['phenix.v1.minimega_node'].properties.hardware.properties;

    expect(DEFAULT_VCPUS).toBe(hardware.vcpus.default);
    expect(DEFAULT_MEMORY_MB).toBe(hardware.memory.default);
  });
});

describe('specWhole', () => {
  test('reads a positive whole number, or text that holds one', () => {
    expect(specWhole(4)).toBe(4);
    expect(specWhole(' 8192 ')).toBe(8192);
    expect(specWhole('+2')).toBe(2);
  });

  test('refuses anything else', () => {
    for (const value of [0, -1, 1.5, '0', '-3', '1.5', '', 'four', null, {}]) {
      expect(specWhole(value)).toBeNull();
    }
    expect(specWhole(2 ** 31)).toBeNull();
  });
});

describe('deviceResources', () => {
  test('takes the defaults for a value that is not set or not valid', () => {
    expect(deviceResources(device('a', { os_type: 'linux' }))).toEqual({
      vcpus: DEFAULT_VCPUS,
      memory: DEFAULT_MEMORY_MB,
    });
    expect(deviceResources(device('a', null))).toEqual({
      vcpus: DEFAULT_VCPUS,
      memory: DEFAULT_MEMORY_MB,
    });
    expect(
      deviceResources(device('a', { vcpus: 'many', memory: '2048' })),
    ).toEqual({ vcpus: DEFAULT_VCPUS, memory: 2048 });
  });
});

describe('startsDevice', () => {
  test('is every device but an external one, included devices too', () => {
    expect(startsDevice(device('a', {}))).toBe(true);
    expect(
      startsDevice(device('a', {}, { device: { includedFrom: 'shared' } })),
    ).toBe(true);
    expect(startsDevice(device('a', {}, { spec: { external: true } }))).toBe(
      false,
    );
    expect(startsDevice({ id: 's', kind: 'switch', switch: {} })).toBe(false);
    expect(startsDevice({ id: 'n', kind: 'note', note: {} })).toBe(false);
  });
});

describe('resourceTotals', () => {
  test('adds the devices phenix starts', () => {
    const nodes = [
      device('a', { vcpus: 2, memory: 2048 }),
      device('b', {}),
      device('c', { vcpus: 8, memory: 8192 }, { spec: { external: true } }),
    ];

    expect(resourceTotals(nodes)).toEqual({
      devices: 2,
      vcpus: 2 + DEFAULT_VCPUS,
      memory: 2048 + DEFAULT_MEMORY_MB,
    });
  });

  test('the totals of two lists are the sum of their totals', () => {
    fc.assert(
      fc.property(devicesArb, fc.nat(30), (nodes, cut) => {
        const whole = resourceTotals(nodes);
        const first = resourceTotals(nodes.slice(0, cut));
        const rest = resourceTotals(nodes.slice(cut));

        expect(whole).toEqual({
          devices: first.devices + rest.devices,
          vcpus: first.vcpus + rest.vcpus,
          memory: first.memory + rest.memory,
        });
      }),
      NUMBER_RUNS,
    );
  });

  test('a node given twice counts once', () => {
    fc.assert(
      fc.property(devicesArb, (nodes) => {
        expect(resourceTotals([...nodes, ...nodes])).toEqual(
          resourceTotals(nodes),
        );
      }),
      NUMBER_RUNS,
    );
  });

  test('each device takes at least the least of one vCPU and 1 MB', () => {
    fc.assert(
      fc.property(devicesArb, (nodes) => {
        const { devices, vcpus, memory } = resourceTotals(nodes);

        expect(devices).toBe(nodes.filter(startsDevice).length);
        expect(vcpus).toBeGreaterThanOrEqual(devices);
        expect(memory).toBeGreaterThanOrEqual(devices);
      }),
      NUMBER_RUNS,
    );
  });
});

describe('selectionResources', () => {
  const group = { id: 'g', kind: 'group', group: { label: 'G' } };
  const inner = { id: 'h', kind: 'group', parentId: 'g', group: {} };
  const doc = {
    nodes: [
      group,
      inner,
      device('a', { vcpus: 2, memory: 1024 }, { node: { parentId: 'g' } }),
      device('b', { vcpus: 4, memory: 4096 }, { node: { parentId: 'h' } }),
      device('c', { vcpus: 1, memory: 512 }),
    ],
  };

  test('counts the selected devices and those in a selected group', () => {
    expect(selectionResources(doc, ['c'])).toEqual({
      devices: 1,
      vcpus: 1,
      memory: 512,
    });
    expect(selectionResources(doc, ['g'])).toEqual({
      devices: 2,
      vcpus: 6,
      memory: 5120,
    });
    // A device and its group count it once.
    expect(selectionResources(doc, ['g', 'a', 'c'])).toEqual({
      devices: 3,
      vcpus: 7,
      memory: 5632,
    });
    expect(selectionResources(doc, [])).toEqual({
      devices: 0,
      vcpus: 0,
      memory: 0,
    });
  });

  test('a loop of parents ends', () => {
    const looped = {
      nodes: [
        { id: 'x', kind: 'group', parentId: 'y', group: {} },
        { id: 'y', kind: 'group', parentId: 'x', group: {} },
        device('a', {}, { node: { parentId: 'x' } }),
      ],
    };

    expect(selectionResources(looped, ['z']).devices).toBe(0);
    expect(selectionResources(looped, ['y']).devices).toBe(1);
  });

  test('the union of two selections of devices is the sum of the disjoint parts', () => {
    fc.assert(
      fc.property(
        devicesArb,
        fc.func(fc.constantFrom('left', 'right', 'none')),
        (nodes, side) => {
          const flat = { nodes };
          const pick = (which) =>
            nodes
              .filter((_, index) => side(index) === which)
              .map((node) => node.id);
          const left = selectionResources(flat, pick('left'));
          const right = selectionResources(flat, pick('right'));
          const both = selectionResources(flat, [
            ...pick('left'),
            ...pick('right'),
          ]);

          expect(both).toEqual({
            devices: left.devices + right.devices,
            vcpus: left.vcpus + right.vcpus,
            memory: left.memory + right.memory,
          });
        },
      ),
      NUMBER_RUNS,
    );
  });

  test('selecting every node gives the diagram totals', () => {
    fc.assert(
      fc.property(devicesArb, (nodes) => {
        const flat = { nodes };

        expect(
          selectionResources(
            flat,
            nodes.map((node) => node.id),
          ),
        ).toEqual(diagramResources(flat));
      }),
      NUMBER_RUNS,
    );
  });
});

describe('the disk images of a device’s drives', () => {
  // A device whose drives have the images given, each a name, '' or null.
  const withDrives = (id, images, extra) =>
    device(
      id,
      { os_type: 'linux', drives: images.map((image) => ({ image })) },
      extra,
    );

  test('are the trimmed names of every drive, without empty ones', () => {
    const nodes = [
      withDrives('a', ['ubuntu.qc2', ' kali.qc2 ']),
      withDrives('b', ['ubuntu.qc2', '', null]),
      withDrives('c', ['vyos.qc2'], { spec: { external: true } }),
      device('d', { os_type: 'linux' }),
      { id: 's', kind: 'switch', switch: {} },
    ];

    expect(driveImages(nodes[0])).toEqual(['ubuntu.qc2', 'kali.qc2']);
    expect(driveImages(nodes[1])).toEqual(['ubuntu.qc2']);
    expect(driveImages(nodes[2])).toEqual(['vyos.qc2']);
    expect(driveImages(nodes[3])).toEqual([]);
    expect(driveImages(nodes[4])).toEqual([]);
    expect(driveImages(null)).toEqual([]);
  });

  test('keep the order of the drives', () => {
    const imagesArb = fc.array(
      fc.constantFrom('a.qc2', 'b.qc2', ' a.qc2', 'c.qc2', ''),
      { maxLength: 6 },
    );

    fc.assert(
      fc.property(imagesArb, (images) => {
        expect(driveImages(withDrives('d', images))).toEqual(
          images.map((image) => image.trim()).filter(Boolean),
        );
      }),
      NUMBER_RUNS,
    );
  });
});

describe('formatMemory', () => {
  test('uses MB below 1 GB, then GB and TB with one decimal at most', () => {
    expect(formatMemory(0)).toBe('0 MB');
    expect(formatMemory(512)).toBe('512 MB');
    expect(formatMemory(1023)).toBe('1,023 MB');
    expect(formatMemory(1024)).toBe('1 GB');
    expect(formatMemory(1536)).toBe('1.5 GB');
    expect(formatMemory(2048 + 100)).toBe('2.1 GB');
    expect(formatMemory(1024 * 1024)).toBe('1 TB');
    expect(formatMemory(1024 * 1024 * 2048)).toBe('2,048 TB');
  });

  test('reads 1 GB as 1024 MB, to within the rounding', () => {
    fc.assert(
      fc.property(fc.integer({ min: 1024, max: 1024 * 1024 - 1 }), (mb) => {
        const [number, unit] = formatMemory(mb).split(' ');

        expect(unit).toBe('GB');
        // Half a tenth of a GB at most, which 1280 MB (1.25 GB) shows.
        expect(
          Math.abs(Number(number.replace(/,/g, '')) * 1024 - mb),
        ).toBeLessThanOrEqual(0.05 * 1024 + 1e-6);
      }),
      NUMBER_RUNS,
    );
  });
});

describe('resourcesText', () => {
  test('says the vCPUs and the memory', () => {
    expect(resourcesText({ vcpus: 1, memory: 512 })).toBe('1 vCPU, 512 MB RAM');
    expect(resourcesText({ vcpus: 6, memory: 3072 })).toBe('6 vCPUs, 3 GB RAM');
  });

  test('is its two parts, the vCPUs then the memory', () => {
    expect(resourcesParts({ vcpus: 1, memory: 512 })).toEqual([
      '1 vCPU',
      '512 MB RAM',
    ]);
    expect(resourcesParts({ vcpus: 6, memory: 3072 })).toEqual([
      '6 vCPUs',
      '3 GB RAM',
    ]);
  });
});
