// How errors are shown: a notification that names the failure, then each
// step of the server's error chain that led to it, the innermost cause last
// and in bold.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/notify.js', () => ({ openNotification: vi.fn() }));
const store = vi.hoisted(() => ({ logout: vi.fn() }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));

import { openNotification } from '@/utils/notify.js';
import { showError, useErrorNotification } from '@/utils/errorNotif.js';

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

  it('says nothing of requests canceled on leaving a page', async () => {
    await useErrorNotification({ code: 'ERR_CANCELED', message: 'canceled' });
    expect(console.warn).not.toHaveBeenCalled();
    expect(openNotification).not.toHaveBeenCalled();
  });
});
