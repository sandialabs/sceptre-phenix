// What to say of a request the phēnix server did not answer. Kept apart from
// errorNotif.js and operations.js so both can use it without an import cycle.
import { header, serverShuttingDown } from '@/utils/freshness.js';

export const SHUTTING_DOWN =
  'The phēnix server is shutting down or restarting. Try again when it is available.';
export const UNREACHABLE =
  'Could not reach the phēnix server. It may not be running, or your network connection may have failed.';

// Whether a request failed on the network with no response: not a timeout, a
// cancel, or a request axios would not send.
export function gotNoResponse(error) {
  if (!error || typeof error !== 'object' || error.response) return false;
  if (error.code === 'ERR_NETWORK') return true;
  return !error.code && !!error.request;
}

// Whether a proxy in front of phēnix answered 502 or 504 for it: phēnix stamps
// X-Phenix-Seq on each API answer, including its own 502s.
function proxyGotNoAnswer(error) {
  const response = error?.response;
  if (response?.status !== 502 && response?.status !== 504) return false;
  return !header(response.headers, 'x-phenix-seq');
}

// Whether no phēnix server answered a request: no response, or a proxy's.
export function serverNotAnswering(error) {
  return gotNoResponse(error) || proxyGotNoAnswer(error);
}

// Whether a request failed because the server is away: it is shutting down,
// refused the request with 503, or did not answer.
export function serverAway(error) {
  return (
    serverShuttingDown() ||
    error?.response?.status === 503 ||
    serverNotAnswering(error)
  );
}

// The message for a request that got no response, or null for any other error.
export function noResponseMessage(error) {
  if (!gotNoResponse(error)) return null;
  return serverShuttingDown() ? SHUTTING_DOWN : UNREACHABLE;
}
