import { describe, expect, test } from 'vitest';

import {
  ICON_FILE_NOT_CONVERTED,
  ICON_FILE_NOT_IMAGE,
  ICON_FILE_TOO_LARGE,
  ICON_NAME,
  IconFileError,
  MAX_DOCUMENT_ICONS,
  MAX_ICON_BYTES,
  MAX_ICON_DATA_LENGTH,
  MAX_ICON_FILE_BYTES,
  MAX_ICON_NAME_BYTES,
  MAX_ICON_PIXELS,
  MAX_ICON_UPLOAD_BYTES,
  decodeIconData,
  embedIcons,
  encodeIconData,
  iconCanvasSize,
  iconDetails,
  iconId,
  iconNameFromFile,
  iconPNGProblem,
  iconRefs,
  iconSizeText,
  iconSrc,
  indexIcons,
  isIconName,
  isIconSrc,
  rasterizeIcon,
  resolveIcon,
  settleIcons,
  sortIcons,
  usageText,
} from '@/builder/icons.js';
import { addNode, createDocument } from '@/builder/model.js';
import bundle from '@/builder/schema/builder-v1.schema.json';
import { validateDocument } from '@/builder/validate.js';

import {
  FIXTURE_CHUNKS,
  GRAY,
  GRAY_ALPHA,
  ICON_DATA,
  ICON_KEY,
  LARGEST_KEY,
  RGB,
  RGBA,
  SIGNATURE,
  base64Of,
  bytesOf,
  chunk,
  concat,
  header,
  largest,
  onePixel,
  png,
} from './png.js';

const fixture = bytesOf(ICON_DATA);
const [head, pixels, end] = FIXTURE_CHUNKS;

describe('custom icon limits', () => {
  // The schema bundle is generated from the server's limits
  // (customicons.go), so a limit changed there fails here.
  test('are the ones the schema bundle carries', () => {
    const icon = bundle.$defs.icon;

    expect(MAX_DOCUMENT_ICONS).toBe(bundle.properties.icons.maxProperties);
    expect(MAX_ICON_DATA_LENGTH).toBe(icon.properties.data.maxLength);
    expect(MAX_ICON_DATA_LENGTH).toBe(54616);
    expect(Object.keys(icon.properties)).toEqual(['data']);
    expect(icon.description).toBe(
      `Custom icon: a PNG of at most ${MAX_ICON_PIXELS} by ${MAX_ICON_PIXELS} pixels and ${MAX_ICON_BYTES} bytes.`,
    );
    expect(`^${ICON_NAME.source.slice(1, -1)}$`).toBe(
      bundle.properties.icons.propertyNames.pattern,
    );
    expect(bundle.$defs.iconRef.pattern).toBe(
      `^(${ICON_NAME.source.slice(1, -1)})?$`,
    );
    expect(MAX_ICON_NAME_BYTES).toBe(64);
  });

  test('an icon id is sha256: and the digest of the bytes', () => {
    expect(iconId(fixture)).toBe(ICON_KEY);
    expect(iconId(new Uint8Array())).toMatch(/^sha256:[0-9a-f]{64}$/);
  });

  test('an icon name is what a phenix config name may hold', () => {
    for (const name of [
      'a',
      'plc',
      'PLC',
      'plc-2',
      'plc_2',
      'alice@site',
      'v1.2',
      '...',
      '.hidden',
      'n'.repeat(64),
    ]) {
      expect(isIconName(name), name).toBe(true);
    }

    for (const name of [
      '',
      '.',
      '..',
      'plc icon',
      'plc/icon',
      'ñandú',
      'n'.repeat(65),
      ICON_KEY,
      `plc\n`,
      undefined,
      null,
      7,
    ]) {
      expect(isIconName(name), String(name)).toBe(false);
    }
  });
});

describe('decodeIconData', () => {
  test('decodes strict standard base64', () => {
    expect(decodeIconData(ICON_DATA)).toEqual(fixture);

    // Every length of the last group, and every byte value.
    for (const length of [1, 2, 3, 4, 255, 256, 257]) {
      const bytes = Uint8Array.from({ length }, (_, i) => (i * 37 + 250) % 256);

      expect(decodeIconData(base64Of(bytes)), length).toEqual(bytes);
    }

    const every = Uint8Array.from({ length: 256 }, (_, i) => i);

    expect(base64Of(every)).toMatch(/[+/]/);
    expect(decodeIconData(base64Of(every))).toEqual(every);
  });

  test.each([
    ['nothing', undefined],
    ['null', null],
    ['a number', 7],
    ['bytes', fixture],
    ['empty text', ''],
    ['text that is not base64', '<svg onload=alert(1)>'],
    ['a data URL', `data:image/png;base64,${ICON_DATA}`],
    ['URL-safe base64', ICON_DATA.replace('A', '_')],
    ['base64 without padding', ICON_DATA.replace(/=+$/, '')],
    [
      'base64 with a line break',
      `${ICON_DATA.slice(0, 40)}\n${ICON_DATA.slice(40)}`,
    ],
    ['base64 with a trailing line break', `${ICON_DATA}\n`],
    ['base64 with a space before it', ` ${ICON_DATA}`],
    ['padding in the middle', 'AA==AAAA'],
    ['three padding characters', 'A==='],
    ['only padding', '===='],
    // "gg==" ends the fixture: the last character holds four bits no byte
    // takes, and "h" sets one.
    ['bits set after one byte', `${ICON_DATA.slice(0, -4)}gh==`],
    ['bits set after two bytes', 'AAB='],
  ])('refuses %s', (_, text) => {
    expect(decodeIconData(text)).toBeNull();
  });

  test('bounds the bytes, and measures the text before decoding it', () => {
    const most = largest();
    const over = concat(most, [0]);

    expect(most.length).toBe(MAX_ICON_BYTES);
    expect(base64Of(most).length).toBe(MAX_ICON_DATA_LENGTH);
    expect(decodeIconData(base64Of(most))).toEqual(most);

    // One byte more is as long in base64, so it is the bytes that are
    // counted.
    expect(base64Of(over).length).toBe(MAX_ICON_DATA_LENGTH);
    expect(decodeIconData(base64Of(over))).toBeNull();
    expect(
      decodeIconData('AAAA'.repeat(MAX_ICON_DATA_LENGTH / 4 + 1)),
    ).toBeNull();
    expect(decodeIconData('A'.repeat(50 * 1024 * 1024))).toBeNull();
  });
});

describe('iconPNGProblem', () => {
  const indexed = concat(
    SIGNATURE,
    // 8 bits a pixel, each an index into the palette.
    chunk('IHDR', [0, 0, 0, 1, 0, 0, 0, 1, 8, 3, 0, 0, 0]),
    chunk('PLTE', [0x2f, 0x6f, 0xbf]),
    chunk('tRNS', [0x80]),
    pixels,
    end,
  );

  test.each([
    ['the fixture', fixture],
    [
      'the most pixels',
      png(MAX_ICON_PIXELS, MAX_ICON_PIXELS, [47, 111, 191, 255]),
    ],
    ['not square', png(MAX_ICON_PIXELS, 3)],
    ['a palette with transparency', indexed],
    [
      'pixels in two chunks',
      concat(
        SIGNATURE,
        head,
        chunk('IDAT', pixels.subarray(8, 14)),
        chunk('IDAT', pixels.subarray(14, -4)),
        end,
      ),
    ],
    [
      'grayscale with a transparent shade',
      onePixel(GRAY, [0x80], chunk('tRNS', [0, 0x80])),
    ],
    [
      'color with a transparent color',
      onePixel(RGB, [1, 2, 3], chunk('tRNS', [0, 1, 0, 2, 0, 3])),
    ],
    ['the most bytes', largest()],
  ])('accepts %s', (_, bytes) => {
    expect(iconPNGProblem(bytes)).toBe('');
  });

  test('the largest icon is the one the server tests hold', () => {
    expect(largest().length).toBe(MAX_ICON_BYTES);
    expect(iconId(largest())).toBe(LARGEST_KEY);
  });

  test('reads a view into a larger buffer', () => {
    const buffer = concat([1, 2, 3], fixture, [4, 5]);

    expect(iconPNGProblem(buffer.subarray(3, 3 + fixture.length))).toBe('');
  });

  // The words are the server's (ValidateIconPNG in customicons.go).
  test.each([
    ['empty', new Uint8Array(), 'the image is empty'],
    [
      'one byte too many',
      concat(largest(), [0]),
      'the image is 40961 bytes; the limit is 40960',
    ],
    [
      'a GIF',
      new TextEncoder().encode('GIF89a\x01\x00\x01\x00\x00\x00\x00;'),
      'the image is not a PNG',
    ],
    [
      'a JPEG',
      Uint8Array.of(0xff, 0xd8, 0xff, 0xe0, 0, 16, 74, 70, 73, 70),
      'the image is not a PNG',
    ],
    [
      'an SVG',
      new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg"/>'),
      'the image is not a PNG',
    ],
    ['half a signature', SIGNATURE.subarray(0, 4), 'the image is not a PNG'],
    ['only the signature', SIGNATURE, 'the PNG is cut short'],
    [
      '97 pixels wide',
      png(97, 1),
      'icon is 97 x 1 pixels; the limit is 96 x 96',
    ],
    [
      '97 pixels high',
      png(1, 97),
      'icon is 1 x 97 pixels; the limit is 96 x 96',
    ],
    [
      'no pixels wide',
      concat(SIGNATURE, header(0, 1), pixels, end),
      'icon is 0 x 1 pixels; the limit is 96 x 96',
    ],
    // Refused on its header alone.
    [
      'a header that claims 50000 by 50000 pixels',
      concat(SIGNATURE, header(50000, 50000), end),
      'icon is 50000 x 50000 pixels; the limit is 96 x 96',
    ],
    [
      'a header that claims the most a PNG can',
      concat(SIGNATURE, header(0xffffffff, 0xffffffff), end),
      'icon is 4294967295 x 4294967295 pixels; the limit is 96 x 96',
    ],
    [
      'a text chunk',
      concat(
        SIGNATURE,
        head,
        chunk(
          'tEXt',
          new TextEncoder().encode('Comment\0<script>alert(1)</script>'),
        ),
        pixels,
        end,
      ),
      'the PNG holds a "tEXt" chunk; only IHDR, PLTE, tRNS, IDAT and IEND are allowed',
    ],
    [
      'an animation chunk',
      concat(
        SIGNATURE,
        head,
        chunk('acTL', [0, 0, 0, 1, 0, 0, 0, 0]),
        pixels,
        end,
      ),
      'the PNG holds a "acTL" chunk; only IHDR, PLTE, tRNS, IDAT and IEND are allowed',
    ],
    [
      'a color profile chunk',
      concat(SIGNATURE, head, chunk('iCCP', [120, 0, 0]), pixels, end),
      'the PNG holds a "iCCP" chunk; only IHDR, PLTE, tRNS, IDAT and IEND are allowed',
    ],
    [
      'a chunk type in lower case',
      concat(SIGNATURE, head, chunk('idat'), pixels, end),
      'the PNG holds a "idat" chunk; only IHDR, PLTE, tRNS, IDAT and IEND are allowed',
    ],
    // Bytes of a palette or of a transparency chunk the pixels do not use
    // would be kept without being part of the image.
    [
      'a palette beside pixels that are colors',
      concat(
        SIGNATURE,
        head,
        chunk('PLTE', new TextEncoder().encode('<script>alert(1)</script>!!')),
        pixels,
        end,
      ),
      'the PNG holds a "PLTE" chunk, which its color type does not use',
    ],
    [
      'a palette beside color pixels without alpha',
      onePixel(RGB, [1, 2, 3], chunk('PLTE', [97, 98, 99])),
      'the PNG holds a "PLTE" chunk, which its color type does not use',
    ],
    [
      'a palette beside grayscale pixels',
      onePixel(GRAY, [0x80], chunk('PLTE', [97, 98, 99])),
      'the PNG holds a "PLTE" chunk, which its color type does not use',
    ],
    [
      'a transparent color beside pixels with alpha',
      onePixel(RGBA, [1, 2, 3, 4], chunk('tRNS', [0, 1, 0, 2, 0, 3])),
      'the PNG holds a "tRNS" chunk, which its color type does not use',
    ],
    [
      'a transparent shade beside grayscale pixels with alpha',
      onePixel(GRAY_ALPHA, [1, 2], chunk('tRNS', [0, 1])),
      'the PNG holds a "tRNS" chunk, which its color type does not use',
    ],
    [
      'bytes after the end',
      concat(fixture, new TextEncoder().encode('<html>')),
      'the PNG has data after its end',
    ],
    [
      'a chunk after the end',
      concat(fixture, chunk('IDAT')),
      'the PNG has data after its end',
    ],
    ['no end', fixture.subarray(0, 58), 'the PNG is cut short'],
    ['a chunk cut short', fixture.subarray(0, 53), 'the PNG is cut short'],
    [
      'a chunk longer than the file',
      concat(
        SIGNATURE,
        head,
        [0xff, 0xff, 0xff, 0xff],
        pixels.subarray(4),
        end,
      ),
      'the PNG is cut short',
    ],
    [
      'an end with data',
      concat(fixture.subarray(0, 58), chunk('IEND', [0])),
      'the PNG does not end as a PNG does',
    ],
    [
      'pixels before the header',
      concat(SIGNATURE, pixels, head, end),
      'the PNG does not start with its header',
    ],
    [
      'a header of the wrong length',
      concat(SIGNATURE, chunk('IHDR', head.subarray(8, 20)), pixels, end),
      'the PNG does not start with its header',
    ],
    [
      'two headers',
      concat(SIGNATURE, head, head, pixels, end),
      'the PNG has a second header',
    ],
  ])('refuses %s', (_, bytes, problem) => {
    expect(iconPNGProblem(bytes)).toBe(problem);
  });

  // The checks that need a decoder are the server's alone: it refuses
  // these when it stores the document.
  test('does not check checksums, inflate the pixels or look after them', () => {
    const wrongChecksum = fixture.slice();

    wrongChecksum[wrongChecksum.length - 13] ^= 0xff;

    const corrupt = pixels.slice(8, -4);

    corrupt[6] ^= 0xff;

    expect(iconPNGProblem(wrongChecksum)).toBe('');
    expect(
      iconPNGProblem(concat(SIGNATURE, head, chunk('IDAT', corrupt), end)),
    ).toBe('');
    expect(iconPNGProblem(concat(SIGNATURE, header(1, 1), end))).toBe('');
    expect(
      iconPNGProblem(
        concat(SIGNATURE, head, pixels, chunk('IDAT', [1, 2, 3]), end),
      ),
    ).toBe('');
  });
});

// An icon of its own bytes: a PNG of one color, with its name and its entry.
function iconOf(pixel, name) {
  const bytes = png(2, 2, pixel);
  const data = base64Of(bytes);

  return { name, id: iconId(bytes), data, entry: { data } };
}

const RED = iconOf([255, 0, 0, 255], 'red');
const BLUE = iconOf([0, 0, 255, 255], 'blue');

// A document with a device that names each of `names` as its custom icon.
function documentUsing(names, icons) {
  let doc = createDocument({ id: 'd0000000-0000-4000-8000-000000000001' });

  names.forEach((icon, index) => {
    doc = addNode(doc, {
      kind: 'device',
      hostname: `host-${index}`,
      look: { icon },
    }).doc;
  });

  return icons ? { ...doc, icons } : doc;
}

// The icon library as icons.js sees it: lookup by name or alias, ignoring
// case (see createIconLibrary).
function libraryOf(icons) {
  const index = indexIcons(icons);

  return { lookup: (name) => index.get(String(name).toLowerCase()) || null };
}

const errorsOf = (doc) =>
  validateDocument(doc).filter((issue) => issue.level === 'error');

describe('iconSrc', () => {
  const PREFIX = 'data:image/png;base64,';

  test('is a PNG data URL of the icon’s data', () => {
    const src = iconSrc('plc', { plc: { data: ICON_DATA } });

    expect(src).toBe(`${PREFIX}${ICON_DATA}`);
    expect(isIconSrc(src)).toBe(true);
  });

  // Every node that shows the icon is given its address on every edit: the
  // same text, not one built again.
  test('gives the same address while the data is the same', () => {
    const icons = { red: RED.entry };
    const first = iconSrc('red', icons);

    expect(iconSrc('red', { red: { ...RED.entry } })).toBe(first);
    // Other data under that name gives the address of that data.
    expect(iconSrc('red', { red: BLUE.entry })).toBe(`${PREFIX}${BLUE.data}`);
    expect(iconSrc('red', icons)).toBe(first);
  });

  test('resolves a copy the document carries before the icon library', () => {
    const library = libraryOf([
      { name: 'Red-2', aliases: ['red'], data: BLUE.data },
    ]);

    expect(iconSrc('red', { red: RED.entry }, library)).toBe(
      `${PREFIX}${RED.data}`,
    );
    // The library by an alias, in any case, and by the name it has now.
    expect(iconSrc('RED', null, library)).toBe(`${PREFIX}${BLUE.data}`);
    expect(iconSrc('red-2', {}, library)).toBe(`${PREFIX}${BLUE.data}`);
    // Nothing resolves the name: the node draws its built-in icon.
    expect(iconSrc('green', { red: RED.entry }, library)).toBe('');
    expect(resolveIcon('green', null, library)).toBeNull();
    expect(resolveIcon('red', { red: RED.entry }, library)).toBe(RED.entry);
  });

  test.each([
    ['no name', '', { '': { data: ICON_DATA } }],
    ['a name that is none', 'a b', { 'a b': { data: ICON_DATA } }],
    ['an icon id', ICON_KEY, { [ICON_KEY]: { data: ICON_DATA } }],
    ['a name the icons lack', 'blue', { red: RED.entry }],
    ['no icons', 'red', undefined],
    ['icons that are no object', 'red', 'icons'],
    ['a key of every object', 'constructor', {}],
    ['an entry without data', 'red', { red: { name: 'red' } }],
    ['an entry that is null', 'red', { red: null }],
    ['data that is no text', 'red', { red: { data: 7 } }],
    ['empty data', 'red', { red: { data: '' } }],
    ['data with a space in it', 'red', { red: { data: 'iVBO Rw0K' } }],
    [
      'data that ends a URL and starts markup',
      'red',
      { red: { data: 'AAAA"><script>alert(1)</script>' } },
    ],
    ['data that is a URL', 'red', { red: { data: 'javascript:alert(1)' } }],
    ['URL-safe base64', 'red', { red: { data: 'iVBORw0KGgo-_w==' } }],
    [
      'data past the largest icon',
      'red',
      { red: { data: 'A'.repeat(MAX_ICON_DATA_LENGTH + 4) } },
    ],
    // The browser is given only a PNG the Builder accepts to decode.
    ['base64 that is no image', 'red', { red: { data: 'AAAA' } }],
    [
      'an image of another format',
      'red',
      { red: { data: 'R0lGODlhAQABAAAAACwAAAAAAQABAAA=' } },
    ],
    [
      'a PNG larger than an icon',
      'red',
      { red: { data: base64Of(png(97, 1)) } },
    ],
    [
      'a PNG that says it is 50000 pixels wide',
      'red',
      {
        red: {
          data: base64Of(concat(SIGNATURE, header(50000, 50000), pixels, end)),
        },
      },
    ],
    [
      'a PNG with a text chunk',
      'red',
      {
        red: {
          data: base64Of(
            concat(SIGNATURE, head, chunk('tEXt', [65, 0, 66]), pixels, end),
          ),
        },
      },
    ],
  ])('is nothing for %s', (_, name, icons) => {
    expect(iconSrc(name, icons)).toBe('');
    // And again: what is remembered of the data is that it gives nothing.
    expect(iconSrc(name, icons)).toBe('');
  });

  test('gives an address again once the data is an icon', () => {
    expect(iconSrc('red', { red: { data: 'AAAA' } })).toBe('');
    expect(iconSrc('red', { red: RED.entry })).toBe(`${PREFIX}${RED.data}`);
  });

  test.each([
    ['a script URL', 'javascript:alert(1)'],
    ['an SVG data URL', `data:image/svg+xml;base64,${ICON_DATA}`],
    ['an HTML data URL', `data:text/html;base64,${ICON_DATA}`],
    ['a PNG data URL that is not base64', 'data:image/png,AAAA'],
    ['a server address', '/api/v1/builder/icons/abc'],
    ['an outside address', 'https://example.com/icon.png'],
    ['a data URL with a space in it', `${PREFIX}iVBO Rw0K`],
    ['a data URL with a line break in it', `${PREFIX}iVBO\nRw0K`],
    ['a data URL with nothing after its start', PREFIX],
    ['a data URL with another type in capitals', `DATA:image/png;base64,AAAA`],
    ['nothing', ''],
    ['no text', { toString: () => `${PREFIX}AAAA` }],
  ])('%s is not an address an icon is drawn from', (_, src) => {
    expect(isIconSrc(src)).toBe(false);
  });
});

describe('iconRefs', () => {
  test('names the icons of devices, groups and templates, each once', () => {
    const doc = {
      nodes: [
        { kind: 'device', device: { icon: 'red' } },
        { kind: 'device', device: { icon: 'red' } },
        { kind: 'device', device: { icon: '' } },
        { kind: 'device', device: {} },
        { kind: 'group', group: { icon: 'blue' } },
        { kind: 'switch', switch: { networkId: 'n' } },
        { kind: 'note', note: { text: 'icon' } },
      ],
      templates: [
        { id: 't', device: { icon: 'plc' } },
        { id: 'u', device: { icon: null } },
        { id: 'v' },
      ],
    };

    expect([...iconRefs(doc)]).toEqual(['red', 'blue', 'plc']);
    expect([...iconRefs({})]).toEqual([]);
    expect([...iconRefs(undefined)]).toEqual([]);
  });
});

describe('settleIcons', () => {
  test('leaves a document that carries no copy the object it is', () => {
    const doc = documentUsing(['red']);

    expect(settleIcons(doc, libraryOf([]))).toBe(doc);
    expect(settleIcons(doc)).toBe(doc);
    expect('icons' in doc).toBe(false);
  });

  test('leaves a document whose copies the library lacks the object it is', () => {
    const doc = documentUsing(['red', 'blue'], {
      red: RED.entry,
      blue: BLUE.entry,
    });

    expect(settleIcons(doc, null)).toBe(doc);
    // The library's icon of a name with other bytes: the copy wins.
    expect(
      settleIcons(doc, libraryOf([{ name: 'red', data: BLUE.data }])),
    ).toBe(doc);
  });

  // Nothing is ever copied in: a node names its icon, and the library
  // resolves it.
  test('adds nothing for an icon the document names and lacks', () => {
    const doc = documentUsing(['red']);
    const settled = settleIcons(
      doc,
      libraryOf([{ name: 'red', ...RED.entry }]),
    );

    expect(settled).toBe(doc);
    expect('icons' in settled).toBe(false);
  });

  test('drops a copy the library holds under that name with the same bytes', () => {
    const doc = documentUsing(['red', 'blue', 'old'], {
      red: RED.entry,
      blue: BLUE.entry,
      old: RED.entry,
    });
    const settled = settleIcons(
      doc,
      libraryOf([
        { name: 'RED', data: RED.data },
        { name: 'new', aliases: ['old'], data: RED.data },
        { name: 'blue', data: RED.data },
      ]),
    );

    expect(settled.icons).toEqual({ blue: BLUE.entry });
    expect(settled.nodes).toBe(doc.nodes);
    expect(errorsOf(settled)).toEqual([]);
    // The document given is not changed.
    expect(Object.keys(doc.icons)).toEqual(['red', 'blue', 'old']);
  });

  test('removes a copy no node names any more, and the key with the last one', () => {
    const both = { red: RED.entry, blue: BLUE.entry };
    const one = settleIcons(documentUsing(['blue'], both), null);

    expect(one.icons).toEqual({ blue: BLUE.entry });

    const none = settleIcons(documentUsing([], both), null);

    expect('icons' in none).toBe(false);

    // An empty or null `icons` goes too.
    expect('icons' in settleIcons(documentUsing([], {}), null)).toBe(false);
    expect('icons' in settleIcons(documentUsing([], null), null)).toBe(false);
  });

  test('keeps the icon every node and template names, whether or not it resolves', () => {
    const doc = {
      ...documentUsing(['red', 'blue']),
      templates: [{ id: 't', name: 'T', device: { icon: 'gone', spec: {} } }],
      icons: { gone: RED.entry },
    };
    const settled = settleIcons(doc, libraryOf([]));

    expect(settled).toBe(doc);
    expect(settled.nodes.map((node) => node.device.icon)).toEqual([
      'red',
      'blue',
    ]);
  });

  test('leaves icons of another shape to the validator', () => {
    const doc = { ...documentUsing([]), icons: ['x'] };

    expect(settleIcons(doc, null)).toBe(doc);
  });
});

describe('embedIcons', () => {
  test('carries every icon the document uses, from its copies or the library', () => {
    const doc = {
      ...documentUsing(['red', 'blue', 'red', 'old']),
      templates: [
        {
          id: 'aaaaaaaa-0000-4000-8000-000000000001',
          name: 'T',
          device: { icon: 'plc', spec: { general: { hostname: 'plc-t' } } },
        },
      ],
      icons: { red: RED.entry, unused: BLUE.entry },
    };
    const library = libraryOf([
      { name: 'red', data: BLUE.data },
      { name: 'Blue', data: BLUE.data, canRename: true },
      { name: 'new', aliases: ['old'], data: RED.data },
    ]);
    const embedded = embedIcons(doc, library);

    // The document's copy wins over the library's icon of its name; a name
    // the library knows as an alias is carried under the name the node
    // uses; a name nothing resolves is left out and said.
    expect(embedded.doc.icons).toEqual({
      red: { data: RED.data },
      blue: { data: BLUE.data },
      old: { data: RED.data },
    });
    expect(embedded.missing).toEqual(['plc']);
    expect(embedded.left).toEqual([]);
    expect(errorsOf(embedded.doc)).toEqual([]);
    // The document given is not changed.
    expect(doc.icons).toEqual({ red: RED.entry, unused: BLUE.entry });
  });

  test('a document that uses no icon carries none', () => {
    const embedded = embedIcons(
      { ...documentUsing([]), icons: { red: RED.entry } },
      null,
    );

    expect('icons' in embedded.doc).toBe(false);
    expect(embedded.missing).toEqual([]);
  });

  test('carries at most 50 icons, and says which it left out', () => {
    const many = Array.from({ length: MAX_DOCUMENT_ICONS + 1 }, (_, index) =>
      iconOf([index, 7, 9, 255], `icon-${index}`),
    );
    const embedded = embedIcons(
      documentUsing(many.map((icon) => icon.name)),
      libraryOf(many.map((icon) => ({ name: icon.name, data: icon.data }))),
    );

    expect(Object.keys(embedded.doc.icons)).toHaveLength(MAX_DOCUMENT_ICONS);
    expect(embedded.left).toEqual([many.at(-1).name]);
    expect(errorsOf(embedded.doc)).toEqual([]);
  });
});

describe('the icon library index', () => {
  test('finds an icon by its name and aliases, ignoring case', () => {
    const plc = { name: 'PLC-2', aliases: ['plc', 'Plc-1'], data: RED.data };
    const index = indexIcons([
      plc,
      { name: 'hmi', data: BLUE.data },
      { name: 'a b', data: BLUE.data },
      { name: 'nodata' },
    ]);

    expect(index.get('plc-2')).toBe(plc);
    expect(index.get('plc')).toBe(plc);
    expect(index.get('plc-1')).toBe(plc);
    expect(index.get('hmi').data).toBe(BLUE.data);
    expect(index.has('a b')).toBe(false);
    expect(index.has('nodata')).toBe(false);
  });
});

describe('what a list says of an icon', () => {
  test('its size comes from its data', () => {
    expect(iconDetails(ICON_DATA)).toEqual({ width: 1, height: 1, bytes: 70 });
    expect(iconDetails(base64Of(png(96, 3)))).toMatchObject({
      width: 96,
      height: 3,
    });
    expect(iconDetails('')).toBeNull();
    expect(iconDetails(undefined)).toBeNull();
    expect(iconDetails(base64Of(new Uint8Array([1, 2, 3])))).toBeNull();
    expect(iconDetails(base64Of(png(97, 1)))).toBeNull();
  });

  test('the size is said in pixels and kibibytes', () => {
    expect(iconSizeText({ data: ICON_DATA })).toBe('1 × 1 pixels, 0.1 KiB');
    // A library's icon says its size itself.
    expect(iconSizeText({ width: 96, height: 64, bytes: 5123, data: '' })).toBe(
      '96 × 64 pixels, 5.0 KiB',
    );
    expect(iconSizeText({ data: 'nothing' })).toBe('');
    expect(iconSizeText(undefined)).toBe('');
  });

  test('the library says what the user uploaded of what each user may', () => {
    expect(usageText(null)).toBe('');
    expect(
      usageText({
        maxIcons: 64,
        maxBytes: 1048576,
        usedIcons: 3,
        usedBytes: 20480,
      }),
    ).toBe('You uploaded 3 of 64 icons, 20.0 KiB of 1 MiB.');
    expect(
      usageText({ maxIcons: 8, maxBytes: 65536, usedIcons: 0, usedBytes: 0 }),
    ).toBe('You uploaded 0 of 8 icons, 0.0 KiB of 64.0 KiB.');
    expect(usageText({ maxIcons: 0, maxBytes: 0 })).toBe('');
  });

  test('icons are listed by name, whatever its case', () => {
    const list = [{ name: 'plc' }, { name: 'Alarm' }, { name: 'PLC' }];

    expect(sortIcons(list).map((icon) => icon.name)).toEqual([
      'Alarm',
      'PLC',
      'plc',
    ]);
    // A new list: the one given keeps its order.
    expect(list[0].name).toBe('plc');
  });
});

describe('making an icon of a file', () => {
  test('base64 text of bytes is what decodeIconData reads back', () => {
    expect(encodeIconData(fixture)).toBe(ICON_DATA);
    expect(encodeIconData(new Uint8Array())).toBe('');

    const large = png(96, 96, [1, 2, 3, 4]);
    const bytes = new Uint8Array(40000).map((_, index) => index % 251);

    expect(decodeIconData(encodeIconData(large))).toEqual(large);
    expect(bytesOf(encodeIconData(bytes))).toEqual(bytes);
  });

  // The name an upload proposes is an icon name, or nothing.
  test.each([
    ['plc.png', 'plc'],
    ['my.plc.icon.svg', 'my.plc.icon'],
    ['  spaced name .jpeg', 'spaced-name'],
    ['no-extension', 'no-extension'],
    ['alice@site_2.png', 'alice@site_2'],
    ['.svg', ''],
    ['..png', ''],
    ['', ''],
    [undefined, ''],
    ['tab\there\u0000\u007f.png', 'tab-here'],
    ['Pompe à eau.png', 'Pompe-eau'],
    ['ééé.gif', ''],
    [`${'a'.repeat(70)}.png`, 'a'.repeat(64)],
    [`${'a'.repeat(63)} b.png`, 'a'.repeat(63)],
  ])('the name of %j is %j', (file, name) => {
    expect(iconNameFromFile(file)).toBe(name);
    expect(name === '' || isIconName(name)).toBe(true);
    expect(name.length).toBeLessThanOrEqual(MAX_ICON_NAME_BYTES);
  });

  test.each([
    // A bitmap is never enlarged.
    [
      [16, 16, false],
      [16, 16],
    ],
    [
      [96, 96, false],
      [96, 96],
    ],
    [
      [1, 1, false],
      [1, 1],
    ],
    // A larger one is scaled to fit, in its proportions.
    [
      [192, 96, false],
      [96, 48],
    ],
    [
      [100, 400, false],
      [24, 96],
    ],
    [
      [4000, 3000, false],
      [96, 72],
    ],
    // Each side is at least one pixel.
    [
      [5000, 10, false],
      [96, 1],
    ],
    [
      [3, 9000, false],
      [1, 96],
    ],
    // A vector image is drawn as large as an icon may be.
    [
      [16, 16, true],
      [96, 96],
    ],
    [
      [48, 24, true],
      [96, 48],
    ],
    [
      [300, 150, true],
      [96, 48],
    ],
    // One that states no size counts as square.
    [
      [0, 0, true],
      [96, 96],
    ],
    [
      [0, 0, false],
      [96, 96],
    ],
    [
      [0, 40, true],
      [96, 40],
    ],
  ])('an image of %j is drawn at %j', ([width, height, vector], size) => {
    expect(iconCanvasSize(width, height, vector)).toEqual({
      width: size[0],
      height: size[1],
    });
  });

  // The browser's image, canvas and URL functions, as rasterizeIcon is
  // given them: an image of `natural` size, drawn on a canvas that gives
  // back `output` as its PNG.
  function browser({
    natural = [48, 24],
    output = png(2, 2, [9, 9, 9, 255]),
    load = async () => {},
    alpha = 255,
    draw = () => {},
    blob,
  } = {}) {
    const log = { urls: [], revoked: [], sources: [], canvases: [], drawn: [] };

    return {
      log,
      deps: {
        createObjectURL(source) {
          log.sources.push(source);
          log.urls.push(`blob:test/${log.urls.length}`);

          return log.urls.at(-1);
        },
        revokeObjectURL: (url) => log.revoked.push(url),
        async loadImage(url) {
          await load(url);

          return { naturalWidth: natural[0], naturalHeight: natural[1], url };
        },
        createCanvas(width, height) {
          const context = {
            drawImage(...args) {
              draw();
              log.drawn.push(args);
            },
            getImageData: (...box) => ({
              data: new Uint8ClampedArray(box[2] * box[3] * 4).fill(alpha),
            }),
          };

          log.canvases.push({ width, height, context });

          return {
            getContext: () => context,
            toBlob(done, type) {
              log.type = type;
              done(
                blob === undefined
                  ? { arrayBuffer: async () => output.buffer }
                  : blob,
              );
            },
          };
        },
        BlobCtor: class {
          constructor(parts, options) {
            this.parts = parts;
            this.type = options.type;
          }
        },
      },
    };
  }

  const file = (name, type = '', size = 100) => ({ name, type, size });

  test('draws the image inside 96 pixels and gives the PNG and a name', async () => {
    const output = png(3, 3, [1, 2, 3, 255]);
    const { deps, log } = browser({ natural: [400, 200], output });
    const photo = file('Rack photo.JPG', 'image/jpeg');

    expect(await rasterizeIcon(photo, deps)).toEqual({
      name: 'Rack-photo',
      data: base64Of(output),
    });
    // The file itself is what the browser is given, as an image.
    expect(log.sources).toEqual([photo]);
    expect(log.canvases).toMatchObject([{ width: 96, height: 48 }]);
    expect(log.canvases[0].context.imageSmoothingQuality).toBe('high');
    expect(log.drawn).toEqual([
      [expect.objectContaining({ url: 'blob:test/0' }), 0, 0, 96, 48],
    ]);
    expect(log.type).toBe('image/png');
    expect(log.revoked).toEqual(['blob:test/0']);
  });

  test('a small bitmap keeps its size, and an SVG is drawn as large as an icon may be', async () => {
    const small = browser({ natural: [16, 12] });

    await rasterizeIcon(file('dot.png', 'image/png'), small.deps);
    expect(small.log.canvases).toMatchObject([{ width: 16, height: 12 }]);

    const vector = browser({ natural: [16, 12] });

    await rasterizeIcon(file('dot.svg', 'image/svg+xml'), vector.deps);
    expect(vector.log.canvases).toMatchObject([{ width: 96, height: 72 }]);
  });

  // The browser tells an SVG by its type alone, so a file that has none
  // is given it by its name.
  test('an .svg file with no type is given to the browser as an SVG', async () => {
    const { deps, log } = browser({ natural: [0, 0] });
    const untyped = file('Logo.SVG');

    await rasterizeIcon(untyped, deps);

    expect(log.sources[0]).toMatchObject({
      parts: [untyped],
      type: 'image/svg+xml',
    });
    expect(log.canvases).toMatchObject([{ width: 96, height: 96 }]);

    // Any other file is given as it is, whatever its name or type says.
    const other = browser();
    const typed = file('logo.svg', 'image/png');

    await rasterizeIcon(typed, other.deps);
    expect(other.log.sources).toEqual([typed]);
  });

  test('refuses a file over 5 MiB before it reads it', async () => {
    const { deps, log } = browser();

    await expect(
      rasterizeIcon(
        file('big.png', 'image/png', MAX_ICON_FILE_BYTES + 1),
        deps,
      ),
    ).rejects.toThrow(new IconFileError(ICON_FILE_TOO_LARGE));
    expect(log.urls).toEqual([]);

    await expect(
      rasterizeIcon(file('fits.png', 'image/png', MAX_ICON_FILE_BYTES), deps),
    ).resolves.toBeTruthy();
    expect(ICON_FILE_TOO_LARGE).toBe('The image file is larger than 5 MiB.');
  });

  test('says a file the browser cannot read is not an image', async () => {
    const { deps, log } = browser({
      load: async () => {
        throw new Error('EncodingError');
      },
    });

    await expect(rasterizeIcon(file('notes.txt'), deps)).rejects.toThrow(
      new IconFileError(ICON_FILE_NOT_IMAGE),
    );
    expect(log.canvases).toEqual([]);
    // The address made for the file is given up all the same.
    expect(log.revoked).toEqual(log.urls);
    await expect(rasterizeIcon(null, deps)).rejects.toThrow(
      ICON_FILE_NOT_IMAGE,
    );
    expect(ICON_FILE_NOT_IMAGE).toBe(
      'This file is not an image the browser can read. Use a PNG, JPEG, GIF, WebP or SVG file.',
    );
  });

  test.each([
    ['the canvas gives no PNG', { blob: null }],
    [
      'drawing throws',
      {
        draw: () => {
          throw new Error('SecurityError');
        },
      },
    ],
    // As one browser leaves an SVG that states no size.
    ['nothing was drawn', { alpha: 0 }],
    ['what comes back is no PNG', { output: new Uint8Array([60, 115, 118]) }],
    [
      'the PNG is past what the server takes',
      { output: concat(SIGNATURE, new Uint8Array(MAX_ICON_UPLOAD_BYTES)) },
    ],
  ])('says an image cannot be converted when %s', async (_, options) => {
    const { deps, log } = browser(options);

    await expect(
      rasterizeIcon(file('icon.png', 'image/png'), deps),
    ).rejects.toThrow(new IconFileError(ICON_FILE_NOT_CONVERTED));
    expect(log.revoked).toEqual(log.urls);
    expect(ICON_FILE_NOT_CONVERTED).toBe(
      'This image could not be converted. Save it as a PNG and upload it again.',
    );
  });

  test('takes a PNG of exactly what the server takes', async () => {
    const output = concat(
      SIGNATURE,
      new Uint8Array(MAX_ICON_UPLOAD_BYTES - SIGNATURE.length),
    );
    const { deps } = browser({ output });

    expect(
      (await rasterizeIcon(file('icon.png', 'image/png'), deps)).data,
    ).toBe(base64Of(output));
  });
});
