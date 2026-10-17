import { usePhenixStore } from '@/store.js';
import { escapeHTML } from '@/utils/escapeHTML.js';
import { openNotification } from '@/utils/notify.js';
import { errorSteps } from '@/utils/operations.js';

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
  if (!('response' in error)) {
    message = errorHTML(error.message);
  } else if (error.response.headers.get('content-type') == 'application/json') {
    const msg = error.response.data ?? {};
    message = errorHTML(msg.message ?? error.message, msg.cause);
  } else if (error.response.data) {
    // if the error is for an invalid token, log the user out
    if (
      error.response.status === 401 &&
      String(error.response.data).toLowerCase().includes('invalid')
    ) {
      usePhenixStore().logout();
      message = 'Token was invalid. Logging out';
    } else {
      message = errorHTML(error.response.data);
    }
  } else {
    message = `<b>Unknown Error Occurred: ${escapeHTML(error.response.statusText)}</b>`;
  }

  showErrorNotification(message);
}

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
