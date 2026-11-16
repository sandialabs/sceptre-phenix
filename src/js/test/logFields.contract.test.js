// The Logs page's message parser: a message's text, its key=value fields in
// order (values with spaces in brackets or quotes, keys with dots), and a
// traceback after them.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import { parseLogMessage } from '@/utils/logFields.js';

const AUDIT =
  'experiment started event.kind=event event.module=phenix ' +
  'event.dataset=phenix.audit event.category=host event.type=[start] ' +
  'event.action=experiment-start event.outcome=success user.name=cgoes ' +
  'user.roles=[Global Admin] source.ip=10.10.100.226 ' +
  'network.forwarded_ip=10.15.31.251 http.request.method=POST ' +
  'url.path=/api/v1/experiments/nera/start phenix.experiment.name=nera ' +
  'phenix.duration=1m14.742s';

// the fields as [key, value] pairs
const pairs = (msg) =>
  parseLogMessage(msg).fields.map(({ key, value }) => [key, value]);

describe('parseLogMessage', () => {
  it('splits an audit line into its text and every field in order', () => {
    const { text, fields, trailing } = parseLogMessage(AUDIT);
    expect(text).toBe('experiment started');
    expect(trailing).toBeNull();
    expect(fields.map((f) => [f.key, f.value])).toEqual([
      ['event.kind', 'event'],
      ['event.module', 'phenix'],
      ['event.dataset', 'phenix.audit'],
      ['event.category', 'host'],
      ['event.type', '[start]'],
      ['event.action', 'experiment-start'],
      ['event.outcome', 'success'],
      ['user.name', 'cgoes'],
      ['user.roles', '[Global Admin]'],
      ['source.ip', '10.10.100.226'],
      ['network.forwarded_ip', '10.15.31.251'],
      ['http.request.method', 'POST'],
      ['url.path', '/api/v1/experiments/nera/start'],
      ['phenix.experiment.name', 'nera'],
      ['phenix.duration', '1m14.742s'],
    ]);
  });

  it.each([
    [
      'a quoted value with spaces',
      'vm started msg="a b c" vm.name=host-01',
      [
        ['msg', '"a b c"'],
        ['vm.name', 'host-01'],
      ],
    ],
    [
      'a quoted value with escaped quotes and a key=value inside',
      'said msg="say \\"hi\\" x=1 now" done=true',
      [
        ['msg', '"say \\"hi\\" x=1 now"'],
        ['done', 'true'],
      ],
    ],
    [
      'a Go map with spaces and dots',
      'settings changed values=map[log.level:debug ui.port:3000] by=admin',
      [
        ['values', 'map[log.level:debug ui.port:3000]'],
        ['by', 'admin'],
      ],
    ],
    [
      'brackets that hold a key=value',
      'ran cmd=[vm config x=1] ok=true',
      [
        ['cmd', '[vm config x=1]'],
        ['ok', 'true'],
      ],
    ],
    [
      'an unquoted value with spaces',
      'app failed error=exit status 1: no such file app=scorch',
      [
        ['error', 'exit status 1: no such file'],
        ['app', 'scorch'],
      ],
    ],
    [
      'a value with = in it',
      'GET done url.path=/api/v1/logs?start=2026 status=200',
      [
        ['url.path', '/api/v1/logs?start=2026'],
        ['status', '200'],
      ],
    ],
    [
      'an unclosed bracket',
      'bad value v=[unclosed next=1',
      [
        ['v', '[unclosed'],
        ['next', '1'],
      ],
    ],
    [
      'an empty value',
      'empty k= j=1',
      [
        ['k', ''],
        ['j', '1'],
      ],
    ],
  ])('reads %s', (_, msg, want) => {
    expect(pairs(msg)).toEqual(want);
  });

  it('keeps a traceback after the fields apart', () => {
    const msg =
      'app failed error=exit status 1 app=scorch\n' +
      'Traceback (most recent call last):\n  File "x.py", line 3, a=1';
    expect(parseLogMessage(msg)).toEqual({
      text: 'app failed',
      fields: [
        { key: 'error', value: 'exit status 1' },
        { key: 'app', value: 'scorch' },
      ],
      trailing:
        'Traceback (most recent call last):\n  File "x.py", line 3, a=1',
    });
  });

  it.each([
    ['capture grant revoked', 'capture grant revoked'],
    ['deleted disk ', 'deleted disk'],
    ['a = b and c==d', 'a = b and c==d'],
    ['', ''],
    [undefined, ''],
  ])('shows %j, which has no fields, as text only', (msg, text) => {
    expect(parseLogMessage(msg)).toEqual({ text, fields: [], trailing: null });
  });

  it('reads a message that starts with a field', () => {
    expect(parseLogMessage('k=v j=w')).toEqual({
      text: '',
      fields: [
        { key: 'k', value: 'v' },
        { key: 'j', value: 'w' },
      ],
      trailing: null,
    });
  });
});

// A word of text with no `=`, space or newline, so it never starts a field.
// A word in a value also has no quote or bracket: one left open there can
// take in the next fields, and a value that starts with `=` reads as `==`.
const word = fc.stringMatching(/^[^=\s]{1,6}$/);
const plain = fc.stringMatching(/^[^=\s"[\]{}()]{1,6}$/);
const keyArb = fc.stringMatching(/^[A-Za-z_][\w.-]{0,8}$/);
const valueArb = fc.oneof(
  fc.constant(''),
  fc.stringMatching(/^[^\s"[\]{}()=][^\s"[\]{}()]{0,7}$/),
  fc.array(plain, { minLength: 2, maxLength: 3 }).map((w) => w.join(' ')),
  fc
    .array(fc.oneof(plain, fc.constant('x=1')), { maxLength: 3 })
    .map((w) => `[${w.join(' ')}]`),
  fc
    .array(fc.oneof(fc.stringMatching(/^[^"\\\n]{1,6}$/), fc.constant('\\"')), {
      maxLength: 3,
    })
    .map((parts) => `"${parts.join('')}"`),
);
const messageArb = fc
  .record({
    text: fc.array(word, { maxLength: 4 }).map((w) => w.join(' ')),
    fields: fc.array(fc.record({ key: keyArb, value: valueArb }), {
      maxLength: 5,
    }),
    trailing: fc.option(fc.string({ maxLength: 12 }), { nil: null }),
  })
  .map((m) => (m.fields.length ? m : { ...m, trailing: null }));

// the message the server would send for these parts
const join = ({ text, fields, trailing }) =>
  [text, ...fields.map(({ key, value }) => `${key}=${value}`)]
    .filter((part, i) => i > 0 || part !== '')
    .join(' ') + (trailing === null ? '' : `\n${trailing}`);

it('reads back the text, fields and traceback a message was built from', () => {
  fc.assert(
    fc.property(messageArb, (parts) => {
      expect(parseLogMessage(join(parts))).toEqual(parts);
    }),
    { numRuns: 300 },
  );
});

// characters that start, end and nest fields, for any message
const anyMessage = fc.oneof(
  fc.string(),
  fc
    .array(fc.constantFrom(...'ak.=_ []{}()"\\\n-x'), { maxLength: 40 })
    .map((c) => c.join('')),
);

it('joins the fields of any message back into its end, after its text', () => {
  fc.assert(
    fc.property(anyMessage, (msg) => {
      const { text, fields, trailing } = parseLogMessage(msg);
      if (fields.length === 0) {
        expect(text).toBe(msg.trimEnd());
        return;
      }
      const tail =
        fields.map(({ key, value }) => `${key}=${value}`).join(' ') +
        (trailing === null ? '' : `\n${trailing}`);
      expect(msg.endsWith(tail)).toBe(true);
      expect(msg.slice(0, msg.length - tail.length).trimEnd()).toBe(text);
      for (const { key } of fields) expect(key).toMatch(/^[A-Za-z_][\w.-]*$/);
    }),
    { numRuns: 500 },
  );
});
