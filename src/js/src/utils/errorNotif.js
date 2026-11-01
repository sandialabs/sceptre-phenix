import { usePhenixStore } from '@/store.js';
import { escapeHTML } from '@/utils/escapeHTML.js';
import { openNotification } from '@/utils/notify.js';
import { noResponseMessage } from '@/utils/unreachable.js';

// Shows an error: a request's, an Error, or a message (a string).
export async function useErrorNotification(error) {
  // requests are canceled on purpose when the user leaves a page
  if (error?.code === 'ERR_CANCELED') return;

  // a message rather than an error object
  if (typeof error === 'string' || error == null) {
    error = { message: error ?? 'Unknown error' };
  }

  let message;
  console.warn('Error', error);
  const unreachable = noResponseMessage(error);
  if (unreachable) {
    message = errorHTML(unreachable);
  } else if (!('response' in error)) {
    message = errorHTML(error.message);
  } else {
    message = responseMessage(error);
  }

  showErrorNotification(message);
}

// The notification body, as HTML, for an error the server answered.
function responseMessage(error) {
  const response = error.response;
  const data = response.data;
  const body = data && typeof data === 'object' ? data : null;

  if (tokenInvalid(response.status, body ? body.message : data)) {
    usePhenixStore().logout();
    return 'Token was invalid. Logging out';
  }
  if (body || isJSON(response)) {
    return errorHTML(body?.message ?? error.message, body?.cause);
  }
  if (data) return errorHTML(data);
  return `<b>Unknown Error Occurred: ${escapeHTML(response.statusText)}</b>`;
}

// whether a response's body is the server's JSON Error
const isJSON = (response) =>
  String(response.headers?.get?.('content-type') ?? '').startsWith(
    'application/json',
  );

// whether the server refused the request's token as invalid: a 401 whose
// JSON Error message, or text body, says so
const tokenInvalid = (status, text) =>
  status === 401 &&
  String(text ?? '')
    .toLowerCase()
    .includes('invalid');

const textHTML = (text) =>
  escapeHTML(text).replace(/\n/g, '<br>').replace(/\t/g, '&emsp;');

// The notification body for an error and its optional cause: the error as the
// title, then the cause on its own line and in bold. Both are text.
function errorHTML(text, cause) {
  // spans, not paragraphs or lists: the notification puts this in a <p>
  const message = `<span class="error-title"><b>Error:</b> ${textHTML(text || 'Unknown error')}</span>`;
  if (!cause) return message;
  return `${message}<span class="error-step"><b>${textHTML(cause)}</b></span>`;
}

// message is HTML
function showErrorNotification(message) {
  openNotification({
    type: 'is-danger',
    hasIcon: true,
    position: 'is-top',
    indefinite: true,
    ariaCloseLabel: 'Dismiss error',
    // Buefy renders the notice as an <article>, which may not carry role=alert;
    // wrap the text instead so assistive technology announces it.
    message: `<span role="alert">${message}</span>`,
  });
}
