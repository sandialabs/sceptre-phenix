// The user's own icon library on the server, as the Custom icons dialog
// uses it (see IconDialog.vue).
//
// The dialog is given this through the Inspector (INSPECTOR_ICON_LIBRARY in
// components/builder/inspector/control.js) and never imports the API client
// itself: the Inspector's renderers stay free of it, as the form adapter
// that lists them is.

import {
  builderApi,
  classifyError,
  errorMessage,
  serverSentence,
} from './api.js';

/**
 * What a failed library request says. The server's refusals of an icon are
 * written to be shown ("icon library is full: at most 64 icons"), so its
 * message is used word for word, as a sentence. A session that ended and a
 * server that cannot be reached say what they say everywhere else.
 *
 * @param {object} error axios-like error
 * @returns {string}
 */
export function iconLibraryFailure(error) {
  const kind = classifyError(error);

  return ['unauthenticated', 'offline'].includes(kind)
    ? errorMessage(kind, error)
    : serverSentence(error) || errorMessage(kind, error);
}

/**
 * @param {object} [api] the Builder API client (see createBuilderApi)
 * @returns {{list: Function, upload: Function, remove: Function,
 *   failure: Function}} list() reads the library, as listIcons answers;
 *   upload({name, data}) adds a PNG, as uploadIcon answers; remove(id)
 *   deletes an icon; failure(error) says why one of them failed
 */
export function createIconLibrary(api = builderApi) {
  return {
    list: () => api.listIcons(),
    upload: (icon) => api.uploadIcon(icon),
    remove: (id) => api.deleteIcon(id),
    failure: iconLibraryFailure,
  };
}

export const iconLibrary = createIconLibrary();
