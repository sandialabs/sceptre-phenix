import { describe, expect, test } from 'vitest';

import {
  LEGACY_SOURCE,
  fileBaseName,
  legacyDiagramHint,
  parseErrorText,
  readChosenFile,
  useFieldError,
  useMessage,
} from '@/components/builder/dialogs/message.js';
import { MAX_DOCUMENT_BYTES } from '@/builder/decode.js';

describe('dialog messages', () => {
  // A live region announces a change to its content; the same text set again
  // must still render a new node, or a repeated mistake is met with silence.
  test('setting the same text again gives it a new key', () => {
    const message = useMessage();
    message.set('Choose a scenario.', 'name');
    const first = message.key;

    message.clear();
    message.set('Choose a scenario.', 'name');

    expect(message.text).toBe('Choose a scenario.');
    expect(message.field).toBe('name');
    expect(message.key).not.toBe(first);
  });

  test('clearing removes the text and the field it was about', () => {
    const message = useMessage();
    message.set('The uploaded file is larger than the 5 MiB limit.', 'file');
    message.clear();

    expect(message.text).toBe('');
    expect(message.field).toBe('');
  });

  test('an empty message is about no field', () => {
    const message = useMessage();
    message.set('', 'file');

    expect(message.field).toBe('');
  });

  test('a field error marks and describes only the control it is about', async () => {
    const { error, invalid, describedBy, fail } = useFieldError('form-error');

    expect(describedBy('file', 'file-hint')).toBe('file-hint');
    expect(describedBy('text')).toBeUndefined();

    await fail('Choose a file to upload.', 'file');

    expect(error.text).toBe('Choose a file to upload.');
    expect(invalid('file')).toBe('true');
    expect(invalid('text')).toBeUndefined();
    expect(describedBy('file', 'file-hint')).toBe('file-hint form-error');
    expect(describedBy('text')).toBeUndefined();

    error.clear();
    expect(invalid('file')).toBeUndefined();
    expect(describedBy('file', 'file-hint')).toBe('file-hint');
  });

  test('parser errors keep their first line, with the position in words', () => {
    const raw =
      'Could not parse the document: unexpected end of the stream within a flow collection (2:1)\n\n 1 | { not json\n 2 | \n-----^';

    expect(parseErrorText(raw)).toBe(
      'Could not parse the document: unexpected end of the stream within a flow collection (line 2, column 1)',
    );
    expect(parseErrorText('Nothing to upload: the document is empty.')).toBe(
      'Nothing to upload: the document is empty.',
    );
    expect(parseErrorText(undefined)).toBe('');
  });
});

describe('a legacy diagram', () => {
  // The File and Paste text sources of Upload read Builder documents. XML
  // is none, so they point at the source that converts it.
  test('text that is XML is pointed at the legacy source', () => {
    const hint =
      'This looks like a legacy Builder diagram (XML). Choose "Legacy Builder diagram or Topology" to convert it.';

    expect(LEGACY_SOURCE).toBe('Legacy Builder diagram or Topology');
    expect(legacyDiagramHint('<mxGraphModel><root/></mxGraphModel>')).toBe(
      hint,
    );
    expect(legacyDiagramHint('<?xml version="1.0"?>\n<mxGraphModel/>')).toBe(
      hint,
    );
    // White space and a byte order mark before it do not hide it.
    expect(legacyDiagramHint(' \n\t<root/>')).toBe(hint);
    expect(legacyDiagramHint('\uFEFF<root/>')).toBe(hint);
  });

  test('any other text keeps the message of the document parser', () => {
    for (const text of [
      '',
      '   ',
      '{"nodes": []}',
      'nodes: [unclosed',
      'name: a < b',
      'kind: Topology\nmetadata:\n  annotations:\n    builder-xml: <mxGraphModel/>\n',
      undefined,
      null,
    ]) {
      expect(legacyDiagramHint(text), String(text)).toBe('');
    }
  });

  test('the diagram is named after its file, without the last extension', () => {
    expect(fileBaseName('plant.xml')).toBe('plant');
    expect(fileBaseName('plant.topology.yaml')).toBe('plant.topology');
    expect(fileBaseName('plant')).toBe('plant');
    expect(fileBaseName('plant.')).toBe('plant');
    expect(fileBaseName('Plant floor 2.XML')).toBe('Plant floor 2');
    // Nothing is left of a name that is only an extension: the server then
    // names the diagram.
    expect(fileBaseName('.xml')).toBe('');
    expect(fileBaseName('')).toBe('');
    expect(fileBaseName(undefined)).toBe('');
  });
});

describe('a chosen file', () => {
  // A file field's change event, choosing a file that holds `text` (and is
  // `size` bytes), named after its text: its text is read once `finish` is
  // called.
  function choose(field, text, size = text?.length) {
    let finish;
    const read = new Promise((resolve) => {
      finish = resolve;
    });

    field.files =
      text === undefined
        ? []
        : [{ name: `${text}.yaml`, size, text: () => read }];

    return { event: { target: field }, finish: () => finish(text) };
  }

  test('is read, or refused when too large, naming what it holds', async () => {
    const field = {};
    const small = choose(field, 'name: lab');
    const read = readChosenFile(small.event, 'file');

    small.finish();
    // With its name, which a draft made from the file records.
    expect(await read).toStrictEqual({
      text: 'name: lab',
      name: 'name: lab.yaml',
    });

    const large = choose(field, 'x', MAX_DOCUMENT_BYTES + 1);

    expect(await readChosenFile(large.event, 'scenario')).toEqual({
      error: 'The uploaded scenario is larger than the 5 MiB limit.',
    });
    // Import names its file as its source does: a config file.
    expect(await readChosenFile(large.event, 'config')).toEqual({
      error: 'The config file is larger than the 5 MiB limit.',
    });
    expect(await readChosenFile(large.event, 'file')).toEqual({
      error: 'The uploaded file is larger than the 5 MiB limit.',
    });
    expect(await readChosenFile(choose(field).event, 'file')).toBeNull();
  });

  // Reading a large file takes a while; choosing another meanwhile must not
  // leave the first one's text in the form.
  test('chosen while another was read replaces it', async () => {
    const field = {};
    const first = choose(field, 'first');
    const firstRead = readChosenFile(first.event, 'file');
    const second = choose(field, 'second');
    const secondRead = readChosenFile(second.event, 'file');

    second.finish();
    first.finish();

    expect(await secondRead).toEqual({ text: 'second', name: 'second.yaml' });
    expect(await firstRead).toBeNull();

    // A file chosen in another field replaces none.
    const mine = choose(field, 'mine');
    const mineRead = readChosenFile(mine.event, 'file');
    const other = choose({}, 'other');
    const otherRead = readChosenFile(other.event, 'file');

    other.finish();
    mine.finish();

    expect(await otherRead).toEqual({ text: 'other', name: 'other.yaml' });
    expect(await mineRead).toEqual({ text: 'mine', name: 'mine.yaml' });
  });
});
