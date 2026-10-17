// How errors are shown: a notification naming the failure, then each step of
// the server's error chain that led to it, the innermost cause last in bold.
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/notify.js', () => ({ openNotification: vi.fn() }));
const store = vi.hoisted(() => ({ logout: vi.fn() }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));

import { openNotification } from '@/utils/notify.js';
import { showError, useErrorNotification } from '@/utils/errorNotif.js';
import { errorSteps } from '@/utils/operations.js';

beforeEach(() => {
  openNotification.mockClear();
  store.logout.mockClear();
  vi.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(() => vi.restoreAllMocks());

// the body of the notification last shown
const message = () => openNotification.mock.lastCall[0].message;

// its lines as HTML: the title, then each step
const lines = () =>
  [
    ...message().matchAll(/<span class="error-(?:title|step)">(.*?)<\/span>/g),
  ].map(([, html]) => html);

// the notification last shown, read by the page's HTML parser
function shown() {
  const box = document.createElement('div');
  box.innerHTML = message();
  const text = (selector) =>
    [...box.querySelectorAll(selector)].map((el) => el.textContent);
  return {
    title: text('.error-title')[0],
    steps: text('.error-step'),
    bold: text('.error-step b'),
    tags: [...box.querySelectorAll('*')].map((el) => el.localName),
  };
}

// a step's text, which the chain's markers do not split: a '*' only inside a
// word, as in vms/*
const stepArb = fc
  .string({ unit: 'binary', minLength: 1, maxLength: 12 })
  .filter(
    (s) =>
      s === s.trim() && !/[\n\t]|(^|\s)\*|\*(\s|$)|errors? occurred|:$/.test(s),
  );

// server text full of markup and the chain's markers
const markupArb = fc
  .array(
    fc.oneof(
      fc.string({ unit: 'binary', maxLength: 4 }),
      fc.constantFrom(
        '<b>',
        '</b>',
        '<span class="error-step">',
        '</span>',
        '<br>',
        '&lt;',
        '&amp;',
        '"',
        '\n',
        '\t',
        ' * ',
        ': 1 error occurred:\n\t* ',
      ),
    ),
    { maxLength: 8 },
  )
  .map((parts) => parts.join(''));

// a step's text as read back from the page: line breaks are <br> elements
// and tabs em spaces
const readBack = (text) => text.replace(/\n/g, '').replace(/\t/g, '\u2003');

// a request error with a response from the server
const failed = ({
  status = 500,
  statusText = '',
  type = 'text/plain',
  data,
}) => ({
  message: `Request failed with status code ${status}`,
  response: {
    status,
    statusText,
    data,
    headers: new Map([['content-type', type]]),
  },
});

describe('showError', () => {
  it('opens a lasting error notification that is announced', () => {
    showError('SCORCH run 0 failed');

    expect(openNotification).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'is-danger',
        hasIcon: true,
        indefinite: true,
      }),
    );
    expect(message()).toMatch(/^<span role="alert">.*<\/span>$/);
  });

  it.each([
    [
      'splits a nested error chain into its steps',
      'failed to execute Scorch run 0 for experiment nera: 1 error occurred:\n\t* running Scorch for experiment nera: 1 error occurred:\n\t* external user component cc failed: 1 error occurred:\n\t* waiting for command to complete: signal: killed\n\n',
      undefined,
      [
        '<b>Error:</b> failed to execute Scorch run 0 for experiment nera',
        'running Scorch for experiment nera',
        'external user component cc failed',
        '<b>waiting for command to complete: signal: killed</b>',
      ],
    ],
    [
      'splits a chain whose newlines were already flattened',
      'run failed: 2 errors occurred: * first * second: killed',
      undefined,
      ['<b>Error:</b> run failed', 'first', '<b>second: killed</b>'],
    ],
    [
      'leaves a plain error whole',
      'experiment not found',
      undefined,
      ['<b>Error:</b> experiment not found'],
    ],
    [
      'adds a separate cause after the steps, keeping its line breaks',
      'bad request',
      'line one\nline <two>',
      ['<b>Error:</b> bad request', '<b>line one<br>line &lt;two&gt;</b>'],
    ],
    [
      'escapes server text so it cannot inject markup',
      '<img src=x onerror=alert(1)>',
      undefined,
      ['<b>Error:</b> &lt;img src=x onerror=alert(1)&gt;'],
    ],
    [
      'names an error with no text',
      '',
      undefined,
      ['<b>Error:</b> Unknown error'],
    ],
  ])('%s', (_, title, detail, want) => {
    showError(title, detail);
    expect(lines()).toEqual(want);
  });

  it('shows each step of any chain, as the server wrote it', () => {
    fc.assert(
      fc.property(
        fc.array(stepArb, { minLength: 1, maxLength: 3 }),
        fc.array(stepArb, { minLength: 1, maxLength: 3 }),
        fc.boolean(),
        (outer, inner, flattened) => {
          // as Go's multierror formats a chain
          const n = inner.length;
          const text =
            outer.join(': 1 error occurred:\n\t* ') +
            `: ${n} error${n > 1 ? 's' : ''} occurred:\n\t* ` +
            inner.join('\n\t* ') +
            '\n\n';
          showError(flattened ? text.replace(/\n\t?/g, ' ') : text);

          const [title, ...steps] = [...outer, ...inner];
          expect(shown()).toMatchObject({
            title: `Error: ${title}`,
            steps,
            bold: [steps.at(-1)],
          });
        },
      ),
    );
  });

  it('draws no markup from any server text', () => {
    fc.assert(
      fc.property(
        markupArb,
        fc.option(markupArb, { nil: undefined }),
        (title, detail) => {
          showError(title, detail);
          const steps = [...errorSteps(title), ...errorSteps(detail)];
          const head = steps.shift() ?? 'Unknown error';
          const breaks = [head, ...steps].join('').split('\n').length - 1;
          const count = (tag) => shown().tags.filter((t) => t === tag).length;

          expect(shown()).toMatchObject({
            title: `Error: ${readBack(head)}`,
            steps: steps.map(readBack),
            bold: steps.slice(-1).map(readBack),
          });
          // the alert, the title and one per step
          expect(count('span')).toBe(2 + steps.length);
          expect(count('b')).toBe(steps.length ? 2 : 1);
          expect(count('br')).toBe(breaks);
          expect(shown().tags).toHaveLength(
            count('span') + count('b') + breaks,
          );
        },
      ),
    );
  });

  it('keeps a lone * in a single error whole', () => {
    showError('role * may not get vms');
    expect(shown()).toMatchObject({
      title: 'Error: role * may not get vms',
      steps: [],
    });
  });
});

describe('useErrorNotification', () => {
  it.each([
    [
      'shows the message of a request that got no answer',
      { message: 'Network Error' },
      ['<b>Error:</b> Network Error'],
    ],
    [
      "shows a JSON error's message and cause",
      failed({
        type: 'application/json',
        data: {
          message: 'starting experiment: 1 error occurred:\n\t* VM web',
          cause: 'disk <missing>',
        },
      }),
      [
        '<b>Error:</b> starting experiment',
        'VM web',
        '<b>disk &lt;missing&gt;</b>',
      ],
    ],
    [
      'shows a text error body',
      failed({ status: 404, data: 'experiment not found' }),
      ['<b>Error:</b> experiment not found'],
    ],
  ])('%s', async (_, error, want) => {
    await useErrorNotification(error);
    expect(lines()).toEqual(want);
  });

  it('names the status of an error without a body', async () => {
    await useErrorNotification(
      failed({ status: 502, statusText: 'Bad <Gateway>' }),
    );
    expect(message()).toContain(
      '<b>Unknown Error Occurred: Bad &lt;Gateway&gt;</b>',
    );
  });

  it('signs the user out when the server says the token is invalid', async () => {
    await useErrorNotification(failed({ status: 401, data: 'Invalid token' }));
    expect(store.logout).toHaveBeenCalled();
    expect(message()).toContain('Token was invalid. Logging out');
  });

  it('shows a message given as a string', async () => {
    await useErrorNotification('Error uploading: disk <full>');
    expect(lines()).toEqual([
      '<b>Error:</b> Error uploading: disk &lt;full&gt;',
    ]);
  });

  it('shows any error, in each shape a request or caller gives one', async () => {
    const text = fc.string({ unit: 'binary' });
    const status = fc.integer({ min: 400, max: 599 });
    const errorArb = fc.oneof(
      text,
      fc.constantFrom(null, undefined),
      fc.record({ message: text }, { requiredKeys: [] }),
      fc
        .oneof(
          fc.record({
            status,
            statusText: text,
            type: fc.constantFrom('text/plain', 'application/json'),
            data: fc.oneof(
              text,
              fc.record(
                { message: text, cause: text },
                { requiredKeys: [], noNullPrototype: true },
              ),
            ),
          }),
          // no body
          fc.record({
            status,
            statusText: text,
            type: fc.constantFrom('text/html', 'application/json'),
            data: fc.constantFrom(undefined, null),
          }),
        )
        .map(failed),
    );
    await fc.assert(
      fc.asyncProperty(errorArb, async (error) => {
        openNotification.mockClear();
        await useErrorNotification(error);
        expect(openNotification).toHaveBeenCalledOnce();
        expect(message()).toMatch(/^<span role="alert">.*<\/span>$/s);
      }),
    );
  });

  it.each([undefined, null])(
    'shows a JSON error with a %s body by its request message',
    async (data) => {
      await useErrorNotification(failed({ type: 'application/json', data }));
      expect(openNotification).toHaveBeenCalledOnce();
      expect(lines()).toEqual([
        '<b>Error:</b> Request failed with status code 500',
      ]);
    },
  );

  it('says nothing of requests canceled on leaving a page', async () => {
    await useErrorNotification({ code: 'ERR_CANCELED', message: 'canceled' });
    expect(console.warn).not.toHaveBeenCalled();
    expect(openNotification).not.toHaveBeenCalled();
  });
});
