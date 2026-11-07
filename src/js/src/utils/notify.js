import { NotificationProgrammatic } from 'buefy';

// the app Buefy is installed on; programmatic components opened outside a
// component need it to resolve globally registered components such as the
// font-awesome-icon their icons render with
let buefyApp;

export function setNotificationApp(app) {
  buefyApp = app;
}

// Opens a Buefy notification from outside a component.
export function openNotification(params) {
  return new NotificationProgrammatic(buefyApp).open(params);
}
