import { NotificationProgrammatic as Notification } from 'buefy';

export async function useErrorNotification(error) {
  let message = null;
  if (!('response' in error)) {
    message = error.message;
  } else if (error.response.headers.get('content-type') == 'application/json') {
    let resp = await fetch(error.body.url);
    let msg = resp.body;

    message = `<h2><b>Error:</b> ${msg.message}</h2>`;

    if (msg.metadata) {
      let cause = msg.metadata.cause
        .replace(/\n/g, '<br>')
        .replace(/\t/g, '&emsp;');
      message = `${message}<br><b>Cause:</b> ${cause}`;
    }
  } else if (error.response.data) {
    message = `<b>Error:</b> ${error.response.data}`;
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
