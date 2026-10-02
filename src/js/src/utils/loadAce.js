// Loads the Ace editor with everything the config editor uses. Once Ace itself
// has loaded, its other pieces are fetched in parallel rather than one after
// another, and the theme and modes are bundled rather than fetched by Ace when
// first used, so opening the editor over a slow link waits on two rounds of
// requests. The Configs list starts this on hover, so the editor usually opens
// with Ace already loaded.
let loading = null;

export function loadAce() {
  loading ??= (async () => {
    // the other pieces register themselves with the global Ace this defines
    const ace = (await import('ace-builds/src-noconflict/ace')).default;

    await Promise.all([
      import('ace-builds/src-noconflict/theme-dracula'),
      import('ace-builds/src-noconflict/mode-json'),
      import('ace-builds/src-noconflict/mode-yaml'),
      import('ace-builds/src-noconflict/keybinding-vim'),
      import('ace-builds/src-noconflict/ext-language_tools'),
    ]);

    return ace;
  })();

  // let a failed load be retried
  loading.catch(() => (loading = null));

  return loading;
}
