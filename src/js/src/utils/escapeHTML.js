// Escapes text for the HTML that Buefy dialogs and notifications render,
// whose names and messages come from users, files and the server.
export const escapeHTML = (text) =>
  String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
