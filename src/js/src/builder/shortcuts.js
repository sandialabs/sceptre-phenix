// The shortcut sheet and its customization (BuilderShortcuts.vue). keymap.js
// has the keys and their storage. A command left with its default keys is
// reset, not stored, so it follows the defaults if they change.

import { listOf } from './announce.js';
import {
  COMMANDS,
  GROUPS,
  commandKeys,
  defaultKeys,
  getCommand,
  isCustomizable,
  shortcutConflicts,
  takesLetters,
  worksInTextFields,
} from './commands.js';
import {
  currentPlatform,
  isCharacterKey,
  keyCaps,
  keyLabel,
  keyRefusal,
  keyText,
  keymapState,
  normalizeKey,
  parseKey,
  resetShortcut,
  sameKey,
  setShortcut,
  shortcutOverride,
  spokenKey,
  typesCharacter,
} from './keymap.js';

// --- where the keys work -------------------------------------------------------

// Where a command's keys work, in words: 'Editor, not in text fields',
// 'Editor and Drafts page, text fields too', 'Canvas'.
export function whereText(idOrCommand) {
  const command = getCommand(idOrCommand) || {};
  const views = command.views || ['editor'];
  const scopes = [].concat(command.scope || 'editor');

  if (scopes.includes('fields') || scopes.includes('editor')) {
    const place = [
      views.includes('editor') && 'Editor',
      views.includes('landing') && 'Drafts page',
    ]
      .filter(Boolean)
      .join(' and ');
    const where = place.charAt(0).toUpperCase() + place.slice(1);

    return scopes.includes('fields')
      ? `${where}, text fields too`
      : `${where}, not in text fields`;
  }

  return scopes.includes('canvas') ? 'Canvas' : '';
}

/** A command's title without the '…' of a command that opens a dialog. */
export function commandName(idOrCommand) {
  return String(getCommand(idOrCommand)?.title || '').replace(/…$/, '');
}

// --- rows ----------------------------------------------------------------------

// Other words people type for the modifiers, so the filter finds '⌘G' by
// "cmd g" as well as "command g".
const MODIFIER_WORDS = {
  Command: 'cmd',
  Option: 'alt opt',
  Control: 'ctrl',
  Ctrl: 'control',
};

// A key as the sheet shows it: its caps, its words for screen readers, and
// whether the single-key switch turned it off.
function keyEntry(spec, platform = currentPlatform()) {
  return {
    spec,
    caps: keyCaps(spec, platform),
    label: keyLabel(spec, platform),
    spoken: spokenKey(spec, platform),
    off: !keymapState.singleKeys && isCharacterKey(spec),
  };
}

function sameKeys(a, b, platform) {
  return (
    a.length === b.length &&
    a.every((key) => b.some((other) => sameKey(key, other, platform)))
  );
}

// One command as a sheet row. `changed` is true when its keys are not the
// defaults, and `defaults` lists the default keys as entries.
export function shortcutRow(idOrCommand, platform = currentPlatform()) {
  const command = getCommand(idOrCommand);
  const keys = commandKeys(command, { platform, all: true }).map((key) =>
    keyEntry(key, platform),
  );
  const defaults = defaultKeys(command, platform);
  const customizable = isCustomizable(command);
  const where = whereText(command);
  const words = keys.flatMap((key) => [
    key.label,
    keyText(key.spec, platform),
    key.spoken,
    ...key.spoken
      .split('+')
      .map((part) => MODIFIER_WORDS[part])
      .filter(Boolean),
  ]);

  return {
    id: command.id,
    title: command.title,
    name: commandName(command),
    group: command.group,
    where,
    keys,
    // Every cap of every key, for the filter's one-character words.
    caps: new Set(
      keys.flatMap((key) => key.caps.map((cap) => cap.toLowerCase())),
    ),
    customizable,
    changed:
      customizable &&
      shortcutOverride(command.id) !== undefined &&
      !sameKeys(
        keys.map((key) => key.spec),
        defaults,
        platform,
      ),
    defaults: defaults.map((key) => keyEntry(key, platform)),
    text: [
      command.title,
      command.group,
      where,
      ...(command.aliases || []),
      ...(command.keywords || []),
    ]
      .concat(words)
      .join(' ')
      .toLowerCase(),
  };
}

// A word of one character is a key ('g' finds ⌘G and ⇧⌘G, '?' the sheet's
// own key). A longer word may appear anywhere in the row.
function rowMatches(row, word) {
  if ([...word].length === 1) {
    return row.caps.has(word === '-' ? '−' : word);
  }

  return row.text.includes(word);
}

// The sheet's rows by group: commands with keys, or with `customize` every
// command whose keys can change. Each word of `query` must match. A word of one
// character matches a key cap, a longer one the title, group, aliases, keywords
// or keys. `keep` lists the command being recorded whatever the query.
export function shortcutGroups({
  query = '',
  customize = false,
  keep = '',
  platform = currentPlatform(),
} = {}) {
  const words = String(query).toLowerCase().split(/\s+/).filter(Boolean);
  const rows = COMMANDS.filter((command) =>
    customize
      ? isCustomizable(command)
      : commandKeys(command, { platform, all: true }).length > 0,
  )
    .map((command) => shortcutRow(command, platform))
    .filter(
      (row) => row.id === keep || words.every((word) => rowMatches(row, word)),
    );

  return GROUPS.map((group) => ({
    group,
    rows: rows.filter((row) => row.group === group),
  })).filter((group) => group.rows.length > 0);
}

// The one-character shortcuts that the single-key switch turns on and off, as
// the platform labels them: ['?', 'N', '=', '+', '−', '⇧1'].
export function characterKeyLabels(platform = currentPlatform()) {
  const labels = COMMANDS.flatMap((command) =>
    commandKeys(command, { platform, all: true }),
  )
    .filter((key) => isCharacterKey(key))
    .map((key) => keyLabel(key, platform));

  return [...new Set(labels)];
}

// What the single-key switch turns on and off, for the hint beside it in the
// shortcut sheet and the Settings dialog.
export function singleKeyHint(platform = currentPlatform()) {
  const labels = characterKeyLabels(platform);
  const mod = platform === 'mac' ? '⌘' : 'Ctrl';

  return labels.length
    ? `${listOf(labels)} work alone, without ${mod}. Set the switch to off if speech input or your screen reader might press them accidentally.`
    : 'No shortcut is a single key now.';
}

// --- recording -------------------------------------------------------------------

// On Windows and Linux the Windows (Super) key belongs to the system.
// Windows keeps almost every shortcut with it, and Linux desktops keep
// most. So the page seldom gets them.
function systemKeyReason(spec, platform) {
  const parts = parseKey(spec);

  return parts?.meta && platform !== 'mac'
    ? `Builder does not use ${keyLabel(spec, platform)}: Windows and Linux reserve most shortcuts with the Windows key.`
    : '';
}

// A command that works in text fields (the palette, Save now) cannot take a
// key that types a character there (typesCharacter: on macOS, also a key
// pressed with ⌥). The field keeps such keys (see dispatchKeydown). So the
// shortcut would not work where the command promises to work, and it would
// block that character where it did work.
function typedKeyReason(command, spec, platform) {
  if (
    !command ||
    !worksInTextFields(command) ||
    !typesCharacter(spec, platform)
  ) {
    return '';
  }

  const mods = platform === 'mac' ? '⌘ or ⌃' : 'Ctrl or Alt';

  return `${keyLabel(spec, platform)} types a character in text fields. ${commandName(command)} also works in text fields. For this reason, its shortcut needs ${mods}.`;
}

// What giving a command a key would do. `status` is 'refused' (`reason` says
// why), 'fixed' (a command with fixed keys has it), 'conflict' (other commands
// have it where this one works), 'same' or 'free'. `inactive` marks a
// one-character key while the single-key switch is off. `others` counts the
// command's other keys, which this key replaces.
export function judgeShortcut(
  idOrCommand,
  spec,
  { platform = currentPlatform() } = {},
) {
  const command = getCommand(idOrCommand);
  const key = normalizeKey(spec);
  const current = commandKeys(command, { platform, all: true });
  const verdict = {
    status: 'free',
    spec: key,
    label: key ? keyText(key, platform) : '',
    spoken: key ? spokenKey(key, platform) : '',
    reason:
      keyRefusal(spec, platform, { letters: takesLetters(command) }) ||
      typedKeyReason(command, key, platform) ||
      systemKeyReason(key, platform),
    conflicts: [],
    fixed: [],
    inactive: Boolean(key) && isCharacterKey(key) && !keymapState.singleKeys,
    others: current.filter((other) => !sameKey(other, key, platform)).length,
  };

  if (verdict.reason) {
    return { ...verdict, status: 'refused' };
  }

  const clashes = shortcutConflicts(command, key, { platform });

  verdict.fixed = clashes.filter((other) => !isCustomizable(other));
  verdict.conflicts = clashes.filter((other) => isCustomizable(other));

  if (verdict.fixed.length) {
    return { ...verdict, status: 'fixed' };
  }
  if (verdict.conflicts.length) {
    return { ...verdict, status: 'conflict' };
  }
  if (current.some((other) => sameKey(other, key, platform))) {
    return { ...verdict, status: 'same' };
  }

  return verdict;
}

// Stores a command's keys, or resets it when they are its defaults.
function storeKeys(command, keys, platform, storage) {
  if (sameKeys(keys, defaultKeys(command, platform), platform)) {
    resetShortcut(command.id, storage);
  } else {
    setShortcut(command.id, keys, storage);
  }
}

// Makes a key a command's only shortcut. It takes the key from other commands
// only with `take`, and `taken` names them with the keys they have left.
export function assignShortcut(
  idOrCommand,
  spec,
  { take = false, platform = currentPlatform(), storage } = {},
) {
  const command = getCommand(idOrCommand);
  const verdict = judgeShortcut(command, spec, { platform });
  const allowed =
    verdict.status === 'free' ||
    verdict.status === 'same' ||
    (verdict.status === 'conflict' && take);

  if (!command || !isCustomizable(command) || !allowed) {
    return { ok: false, verdict, taken: [] };
  }

  const taken = verdict.conflicts.map((other) => {
    const keys = commandKeys(other, { platform, all: true }).filter(
      (key) => !sameKey(key, verdict.spec, platform),
    );

    storeKeys(other, keys, platform, storage);

    return { id: other.id, name: commandName(other), keys };
  });

  storeKeys(command, [verdict.spec], platform, storage);

  return { ok: true, verdict, taken };
}

// Leaves a command without a shortcut, or at its defaults when it has none by
// default.
export function removeShortcut(
  idOrCommand,
  { platform = currentPlatform(), storage } = {},
) {
  const command = getCommand(idOrCommand);

  if (isCustomizable(command)) {
    storeKeys(command, [], platform, storage);
  }
}

// The keys as words for screen readers: 'Shift+Command+Z', 'Ctrl+Shift+Z or
// Ctrl+Y', 'no shortcut'.
export function spokenKeys(keys, platform = currentPlatform()) {
  const words = keys.map((key) => spokenKey(key, platform));

  if (!words.length) {
    return 'no shortcut';
  }

  return words.length === 1
    ? words[0]
    : `${words.slice(0, -1).join(', ')} or ${words.at(-1)}`;
}

// Where a reason names a key: the first time its label is a separate word,
// so the key L is not found in "Letters".
function labelAt(reason, label) {
  for (
    let at = reason.indexOf(label);
    at >= 0;
    at = reason.indexOf(label, at + 1)
  ) {
    const before = reason[at - 1] || ' ';
    const after = reason[at + label.length] || ' ';

    if (!/\w/.test(before) && !/\w/.test(after)) {
      return at;
    }
  }

  return -1;
}

// What the recorder says about a verdict, as text parts and the recorded key
// ({key: true}), which shows as key caps. `tone` is info, warning or error.
export function recorderMessage(verdict, name, platform = currentPlatform()) {
  const key = { key: true };
  let tone = 'info';
  let parts;

  switch (verdict.status) {
    case 'refused': {
      // A reason that names the key names it by its label, which becomes
      // the key part. A reason that does not name the key comes after it.
      const label = verdict.spec ? keyLabel(verdict.spec, platform) : '';
      const at = label ? labelAt(verdict.reason, label) : -1;
      const pieces =
        at >= 0
          ? [
              verdict.reason.slice(0, at),
              verdict.reason.slice(at + label.length),
            ]
          : [label ? `: ${verdict.reason}` : verdict.reason];

      tone = 'error';
      parts = pieces.flatMap((text, index) =>
        index || (label && pieces.length === 1) ? [key, { text }] : [{ text }],
      );
      parts.push({ text: ' Press another shortcut.' });
      break;
    }
    case 'fixed':
      tone = 'error';
      parts = [
        key,
        {
          text: ` already runs ${listOf(verdict.fixed.map(commandName))}, which cannot change. Press another shortcut.`,
        },
      ];
      break;
    case 'conflict':
      tone = 'warning';
      parts = [
        key,
        {
          text: ` already runs ${listOf(verdict.conflicts.map(commandName))}. Select Use for ${name} to move it, or press another shortcut.`,
        },
      ];
      break;
    case 'same':
      parts = [
        { text: `${name} already uses ` },
        key,
        {
          text: verdict.others
            ? '. Press Enter to make it the only one, or press another shortcut.'
            : '. Press Enter to keep it, or press another shortcut.',
        },
      ];
      break;
    default:
      parts = [
        key,
        {
          text: verdict.others
            ? ' is free. Press Enter to use it instead, or Escape to cancel.'
            : ' is free. Press Enter to keep it, or Escape to cancel.',
        },
      ];
  }

  if (verdict.inactive && verdict.status !== 'refused') {
    parts.push({
      text: ' Single-key shortcuts are off. It works only when they are on.',
    });
  }

  return {
    tone,
    parts,
    spoken: parts
      .map((part) => (part.key ? verdict.spoken : part.text))
      .join(''),
  };
}
