// What the UI says of a request the phēnix server did not answer.
import { AxiosError } from 'axios';
import { afterEach, describe, expect, it } from 'vitest';

import { setWsDown } from '@/utils/freshness.js';
import {
  gotNoResponse,
  noResponseMessage,
  SHUTTING_DOWN,
  UNREACHABLE,
} from '@/utils/unreachable.js';

afterEach(() => setWsDown(false));

// an error axios rejects with, as it builds them
const axiosError = (code, response) =>
  new AxiosError('failed', code, {}, {}, response);

describe('noResponseMessage', () => {
  it.each([
    ['the live connection is up', null, UNREACHABLE],
    ['the live connection was lost', 'lost', UNREACHABLE],
    ['live updates fell behind', 'behind', UNREACHABLE],
    ['the server said it is shutting down', 'shutdown', SHUTTING_DOWN],
  ])('explains a network failure while %s', (_, cause, want) => {
    if (cause) setWsDown(true, cause);
    expect(noResponseMessage(axiosError('ERR_NETWORK'))).toBe(want);
  });

  it('says the server could not be reached again once the socket is back', () => {
    setWsDown(true, 'shutdown');
    setWsDown(true, 'lost');
    expect(noResponseMessage(axiosError('ERR_NETWORK'))).toBe(SHUTTING_DOWN);
    setWsDown(false);
    expect(noResponseMessage(axiosError('ERR_NETWORK'))).toBe(UNREACHABLE);
  });

  it.each([
    ['a network failure', axiosError('ERR_NETWORK'), true],
    [
      'a request with no response and no code',
      { message: 'failed', request: {} },
      true,
    ],
    [
      'a request the server answered',
      axiosError('ERR_BAD_RESPONSE', { status: 503, data: 'busy' }),
      false,
    ],
    ['a request axios would not send', axiosError('ERR_INVALID_URL'), false],
    ['a timed out request', axiosError('ECONNABORTED'), false],
    ['a canceled request', axiosError('ERR_CANCELED'), false],
    ['an error that is not a request', new Error('Network Error'), false],
    ['a message', 'Network Error', false],
    ['nothing', null, false],
  ])('tells apart %s', (_, error, matched) => {
    setWsDown(true, 'shutdown');
    expect(gotNoResponse(error)).toBe(matched);
    expect(noResponseMessage(error)).toBe(matched ? SHUTTING_DOWN : null);
  });
});
