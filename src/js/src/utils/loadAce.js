// Loads Ace, then in parallel the theme, modes and extensions the config
// editor uses. The Configs list starts it on hover.
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
