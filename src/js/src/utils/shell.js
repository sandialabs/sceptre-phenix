// Words for the commands the UI gives people to run in a POSIX shell.

// a string in single quotes, for a POSIX shell
export const shellQuote = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`;

// a string as one shell word: as it is when the shell reads it so, else quoted
export const shellWord = (s) =>
  /^[\w@%+=:,./-]+$/.test(String(s)) ? String(s) : shellQuote(s);
