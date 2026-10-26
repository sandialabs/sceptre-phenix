// What to say of a request the phēnix server did not answer. Kept apart from
// errorNotif.js and operations.js so both can use it without an import cycle.
import { serverShuttingDown } from '@/utils/freshness.js';

export const SHUTTING_DOWN =
  'The phēnix server is shutting down or restarting. Try again once it is back.';
export const UNREACHABLE =
  'Could not reach the phēnix server. It may be down, or your network may have dropped.';

// Whether a request failed on the network with no response: not a timeout, a
// cancel, or a request axios would not send.
export function gotNoResponse(error) {
  if (!error || typeof error !== 'object' || error.response) return false;
  if (error.code === 'ERR_NETWORK') return true;
  return !error.code && !!error.request;
}

// The message for a request that got no response, or null for any other error.
export function noResponseMessage(error) {
  if (!gotNoResponse(error)) return null;
  return serverShuttingDown() ? SHUTTING_DOWN : UNREACHABLE;
}
