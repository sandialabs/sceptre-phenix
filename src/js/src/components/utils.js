import Vue from 'vue';

import {NotificationProgrammatic as Notification} from 'buefy';

let errorNotification = async (error) => {
  let message = null;

  if (error.headers.get('content-type') == 'application/json') {
    let resp = await Vue.http.get(error.body.url);
    let msg  = resp.body;

    message = `<h2><b>Error:</b> ${msg.message}</h2>`;

    if (msg.metadata) {
      let cause = msg.metadata.cause.replace(/\n/g, '<br>').replace(/\t/g, '&emsp;');
      message   = `${message}<br><b>Cause:</b> ${cause}`;
    }
  } else { message = `<b>Error:</b> ${error.bodyText}` }

  Notification.open({
    type:       'is-danger',
    hasIcon:    true,
    position:   'is-top',
    indefinite: true,
    message:    message
  })
};

export {errorNotification};
