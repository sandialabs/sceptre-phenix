import { usePhenixStore } from '@/store.js';
import { brokenDisks, brokenDisksBody } from '@/utils/brokenDisks.js';
import { escapeHTML } from '@/utils/escapeHTML.js';
import { openNotification } from '@/utils/notify.js';
import { errorBody, errorSteps } from '@/utils/operations.js';
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
    message = await responseMessage(error);
  }

  showErrorNotification(message);
}

// The notification body for an error the server answered, whose JSON Error
// may come as an object, as text, or in a download's Blob: HTML, or a VNode.
async function responseMessage(error) {
  const response = error.response;
  let data = response.data;
  if (data instanceof Blob) data = await data.text().catch(() => '');
  const body = errorBody(data);

  if (tokenInvalid(response.status, body ? body.message : data)) {
    usePhenixStore().logout();
    return 'Token was invalid. Logging out';
  }
  const disks = brokenDisks(body);
  if (disks) return brokenDisksBody(body.message ?? error.message, disks);
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

// The notification body for an error and its optional cause: the outermost
// error as the title, then each step that led to it on its own line, the
// innermost (the actual cause) last and in bold.
function errorHTML(text, cause) {
  const steps = [...errorSteps(text), ...errorSteps(cause)];
  const title = steps.shift() ?? 'Unknown error';

  // spans, not paragraphs or lists: the notification puts this in a <p>
  const message = `<span class="error-title"><b>Error:</b> ${textHTML(title)}</span>`;
  const items = steps.map((step, i) =>
    i === steps.length - 1
      ? `<span class="error-step"><b>${textHTML(step)}</b></span>`
      : `<span class="error-step">${textHTML(step)}</span>`,
  );
  return message + items.join('');
}

// Shows an error that did not come from a request, such as one reported over
// the websocket. title and detail are plain text.
export function showError(title, detail) {
  showErrorNotification(errorHTML(title, detail));
}

// message is HTML, or a VNode that carries its own role=alert
function showErrorNotification(message) {
  openNotification({
    type: 'is-danger',
    hasIcon: true,
    position: 'is-top',
    indefinite: true,
    ariaCloseLabel: 'Dismiss error',
    // Buefy renders the notice as an <article>, which may not carry role=alert;
    // wrap the text instead so assistive technology announces it.
    message:
      typeof message === 'string'
        ? `<span role="alert">${message}</span>`
        : message,
  });
}
