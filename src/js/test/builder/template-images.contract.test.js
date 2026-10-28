// The drive images of the library's templates that the server does not have
// (missingImages in templates.js), and the Node Templates tab's list of them.

import fc from 'fast-check';
import { describe, expect, test } from 'vitest';

import {
  SHOW_BUILTIN,
  SHOW_MISSING_IMAGES,
  missingImages,
  missingImagesText,
  shownList,
} from '@/builder/templates.js';
import { validateDocument } from '@/builder/validate.js';

import { libraryOf, sampleDocument } from './fixtures.js';

function template(id, drives, init = {}) {
  return {
    id,
    source: 'library',
    name: id.toUpperCase(),
    device: {
      iconKey: 'router',
      spec: {
        type: 'VirtualMachine',
        general: { hostname: id, description: '', vm_type: 'kvm' },
        hardware: {
          os_type: 'linux',
          drives: drives.map((image) => ({ image })),
        },
        network: { interfaces: [] },
      },
    },
    ...init,
  };
}

// The images missingImages finds for one template, or [] for none.
function imagesOf(item, disks) {
  return missingImages([item], disks).get(`${item.source}:${item.id}`) || [];
}

describe('missingImages', () => {
  test('matches each drive image by its file name, in the same case', () => {
    const disks = ['ubuntu.qc2', 'kali.qc2'];
    const item = template('alfa', [
      'ubuntu.qc2',
      '/phenix/images/kali.qc2',
      'Ubuntu.qc2',
      'UBUNTU.QC2',
      'missing.qc2',
      '  kali.qc2  ',
    ]);

    expect(imagesOf(item, disks)).toEqual([
      'Ubuntu.qc2',
      'UBUNTU.QC2',
      'missing.qc2',
    ]);
  });

  test('names each image once, in the order of the drives', () => {
    const item = template('alfa', ['b.qc2', 'a.qc2', 'b.qc2', ' a.qc2']);

    expect(imagesOf(item, ['c.qc2'])).toEqual(['b.qc2', 'a.qc2']);
  });

  test('passes a template with no image, no drives, no spec or an external node', () => {
    const blank = template('blank', ['', '   ']);
    const bare = template('bare', []);
    const noSpec = { id: 'nospec', source: 'library', name: 'X', device: {} };
    const noDevice = { id: 'nodevice', source: 'library', name: 'Y' };
    const external = template('ext', ['missing.qc2']);

    external.device.spec.external = true;

    expect(
      missingImages([blank, bare, noSpec, noDevice, external], ['a.qc2']),
    ).toEqual(new Map());
  });

  test('finds nothing while the disk images are unknown', () => {
    const items = [template('alfa', ['missing.qc2'])];

    expect(missingImages(items, null)).toEqual(new Map());
    expect(missingImages(items, undefined)).toEqual(new Map());
  });

  test('keys each template by where it is kept and its id', () => {
    const library = template('alfa', ['gone.qc2']);
    const builtin = template('alfa', ['gone.qc2'], { source: 'builtin' });

    expect([...missingImages([library, builtin], []).keys()]).toEqual([
      'library:alfa',
      'builtin:alfa',
    ]);
  });

  // The drive image check of a diagram (device.image.unknown) reports the
  // same images for a device of the template's spec.
  test('reports what device.image.unknown reports for the same spec', () => {
    const names = ['a.qc2', 'A.qc2', 'b.img', 'dir/a.qc2', 'c.iso'];
    const image = fc.oneof(
      fc.constantFrom(...names, '', ' a.qc2 ', '/x/b.img'),
      fc.string({ maxLength: 6 }),
    );

    fc.assert(
      fc.property(
        fc.array(image, { maxLength: 5 }),
        fc.subarray(names),
        fc.boolean(),
        (drives, disks, external) => {
          const item = template('alfa', drives);
          const sample = sampleDocument();

          item.device.spec.external = external || undefined;

          const doc = {
            ...sample.doc,
            nodes: sample.doc.nodes.map((node) =>
              node.id === sample.bravo.id
                ? {
                    ...node,
                    device: {
                      ...node.device,
                      spec: structuredClone(item.device.spec),
                    },
                  }
                : node,
            ),
          };
          const reported = validateDocument(doc, { disks })
            .filter(
              (issue) =>
                issue.code === 'device.image.unknown' &&
                issue.nodeId === sample.bravo.id,
            )
            .map(
              (issue) =>
                item.device.spec.hardware.drives[
                  Number(issue.field.split('.')[3])
                ].image,
            )
            .map((name) => name.trim());

          expect(imagesOf(item, disks)).toEqual([...new Set(reported)]);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('missingImagesText', () => {
  test('names the images, as the drive image field does', () => {
    expect(missingImagesText(['gone.qc2'])).toBe(
      'The server has no disk image named "gone.qc2".',
    );
    expect(missingImagesText(['a.qc2', 'b.qc2', 'c.iso'])).toBe(
      'The server has no disk images named "a.qc2", "b.qc2" and "c.iso".',
    );
    expect(missingImagesText([])).toBe('');
    expect(missingImagesText(undefined)).toBe('');
  });
});

describe('shownList with SHOW_MISSING_IMAGES', () => {
  const library = libraryOf({
    templates: [
      template('alfa', ['ubuntu.qc2']),
      template('bravo', ['gone.qc2']),
      template('charlie', []),
      template('delta', ['other.qc2', 'ubuntu.qc2']),
    ],
    collections: [
      { id: 'plant', name: 'Plant', templateIds: ['bravo', 'alfa'] },
    ],
  });

  library.items.push(
    template('server-t1', ['gone.qc2'], { source: 'preloaded' }),
  );

  test('lists the templates of the library with a missing image, in order', () => {
    const shown = shownList(library, SHOW_MISSING_IMAGES, {
      disks: ['ubuntu.qc2'],
    });

    expect(shown).toMatchObject({
      source: 'library',
      collection: null,
      missingImages: true,
    });
    expect(shown.templates.map((item) => item.id)).toEqual(['bravo', 'delta']);
  });

  test('lists none when the server has every image', () => {
    const shown = shownList(library, SHOW_MISSING_IMAGES, {
      disks: ['ubuntu.qc2', 'gone.qc2', 'other.qc2'],
    });

    expect(shown.templates).toEqual([]);
  });

  test('names no list while the disk images are unknown', () => {
    expect(shownList(library, SHOW_MISSING_IMAGES)).toBeNull();
    expect(shownList(library, SHOW_MISSING_IMAGES, { disks: null })).toBeNull();
  });

  test('the other lists are not filtered', () => {
    const disks = { disks: ['ubuntu.qc2'] };

    for (const value of ['', 'plant', SHOW_BUILTIN]) {
      const shown = shownList(library, value, disks);

      expect(shown.missingImages).toBe(false);
      expect(shown.templates).toEqual(shownList(library, value).templates);
    }
  });
});
