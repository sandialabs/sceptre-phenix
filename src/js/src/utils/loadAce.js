// Loads the Ace editor with everything the config editor uses: Ace, then its
// other pieces in parallel, with the theme and modes bundled, so a slow link
// waits on two rounds of requests. The Configs list starts it on hover.
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
