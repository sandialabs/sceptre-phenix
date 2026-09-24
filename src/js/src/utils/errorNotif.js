import { usePhenixStore } from '@/store.js';
import { NotificationProgrammatic as Notification } from 'buefy';

export async function useErrorNotification(error) {
  let message;
  console.warn('Error', error);
  if (!('response' in error)) {
    message = error.message;
  } else if (error.response.headers.get('content-type') == 'application/json') {
    let msg = error.response.data;
    message = `<p><strong>Error:</strong> ${msg.message}</p>`;

    if (msg.cause) {
      let cause = msg.cause.replace(/\n/g, '<br>').replace(/\t/g, '&emsp;');
      message = `${message}<br><b>Cause:</b> ${cause}`;
    }
  } else if (error.response.data) {
    // if the error is for an invalid token, log the user out
    if (
      error.response.status === 401 &&
      String(error.response.data).toLowerCase().includes('invalid')
    ) {
      usePhenixStore().logout();
      message = 'Token was invalid. Logging out';
    } else {
      message = `<b>Error:</b> ${error.response.data}`;
    }
  } else {
    message = `<b>Unknown Error Occurred: ${error.response.statusText}</b>`;
  }

  // see: https://github.com/ntohq/buefy-next/issues/248
  // TODO: icon not showing
  new Notification().open({
    type: 'is-danger',
    hasIcon: true,
    position: 'is-top',
    indefinite: true,
    ariaCloseLabel: 'Dismiss error',
    // Buefy renders the notice as an <article>, which may not carry role=alert;
    // wrap the text instead so assistive technology announces it.
    message: `<div role="alert">${message}</div>`,
  });
}
