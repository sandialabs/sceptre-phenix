import Vue from 'vue';

import { NotificationProgrammatic as Notification } from 'buefy';

let errorNotification = async (error) => {
  console.log(error)
  let message = message = "<b>Unknown Error Occurred</b>";

  if (error.body.message) {
    message = `<h2><b>Error:</b> ${error.body.message}</h2>`;
  }

  if (error.body.cause) {
    let cause = error.body.cause.replace(/\n/g, '<br>').replace(/\t/g, '&emsp;');
    message = `${message}<br><b>Cause:</b> ${cause}`;
  }

  Notification.open({
    type: 'is-danger',
    hasIcon: true,
    position: 'is-top',
    indefinite: true,
    message: message
  })
};

export { errorNotification };
