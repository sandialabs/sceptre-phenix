import { usePhenixStore } from '@/store.js';
import { NotificationProgrammatic as Notification } from 'buefy';

export async function useErrorNotification(error) {
  let message = null;
  console.warn('Error', error);
  if (!('response' in error)) {
    message = error.message;
  } else if (error.response.headers.get('content-type') == 'application/json') {
    let msg = {};

    if (!('body' in error)) {
      msg = error.response.data;
      message = `<h2><b>Error:</b> ${msg.message}</h2>`;

      let cause = msg.cause.replace(/\n/g, '<br>').replace(/\t/g, '&emsp;');
      message = `${message}<br><b>Cause:</b> ${cause}`;
    } else {
      let resp = await fetch(error.body.url);
      msg = resp.body;
      message = `<h2><b>Error:</b> ${msg.message}</h2>`;

      if (msg.metadata) {
        let cause = msg.metadata.cause
          .replace(/\n/g, '<br>')
          .replace(/\t/g, '&emsp;');
        message = `${message}<br><b>Cause:</b> ${cause}`;
      }
    }
  } else if (error.response.data) {
    // if the error is for an invalid token, log the user out
    if (
      error.response.status === 401 &&
      error.response.data.toLowerCase().includes('invalid')
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
    message: message,
  });
}
