import { describe, expect, test } from 'vitest';

import {
  parseErrorText,
  useFieldError,
  useMessage,
} from '@/components/builder/dialogs/message.js';

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
