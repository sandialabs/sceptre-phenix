// The shortcut sheet and its customization (BuilderShortcuts.vue): the rows
// the sheet lists, where each command's keys work, the sheet's filter, and
// what the key recorder makes of a key press. The keys, their labels and the
// per-browser storage are keymap.js's; the commands are commands.js's.
//
// A key the recorder is given is judged once (judgeShortcut) and then kept
// (assignShortcut), which takes it from the commands that had it when the
// user chose to. A command left with its default keys is reset rather than
// stored, so it follows the defaults if they change.

import { listOf } from './announce.js';
import {
  COMMANDS,
  GROUPS,
  commandKeys,
  defaultKeys,
  getCommand,
  isCustomizable,
  shortcutConflicts,
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

/**
 * Where a command's keys work, in words: 'Editor, not in text fields',
 * 'Editor and drafts list, text fields too', 'Canvas and outline rows'.
 *
 * @param {string|object} idOrCommand
 * @returns {string}
 */
export function whereText(idOrCommand) {
  const command = getCommand(idOrCommand) || {};
  const views = command.views || ['editor'];
  const scopes = [].concat(command.scope || 'editor');

  if (scopes.includes('fields') || scopes.includes('editor')) {
    const place = [
      views.includes('editor') && 'Editor',
      views.includes('landing') && 'drafts list',
    ]
      .filter(Boolean)
      .join(' and ');
    const where = place.charAt(0).toUpperCase() + place.slice(1);

    return scopes.includes('fields')
      ? `${where}, text fields too`
      : `${where}, not in text fields`;
  }

  const places = [
    scopes.includes('canvas') && 'canvas',
    scopes.includes('outline') && 'outline rows',
  ].filter(Boolean);
  const where = places.join(' and ');

  return where.charAt(0).toUpperCase() + where.slice(1);
}

/**
 * A command's title for running text: without the '…' that marks one that
 * opens a dialog ('Export', not 'Export…').
 *
 * @param {string|object} idOrCommand
 * @returns {string}
 */
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

/**
 * A key as the sheet shows it: its caps, the words screen readers get, and
 * whether the single-key switch has turned it off.
 *
 * @param {string} spec
 * @param {'mac'|'other'} [platform]
 * @returns {{spec: string, caps: string[], label: string, spoken: string,
 *   off: boolean}}
 */
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

/**
 * One command as a row of the sheet. `changed` is true when the user gave
 * it keys other than its defaults; `defaults` are those, as entries.
 *
 * @param {string|object} idOrCommand
 * @param {'mac'|'other'} [platform]
 * @returns {object}
 */
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
    text: [command.title, command.group, where, ...(command.keywords || [])]
      .concat(words)
      .join(' ')
      .toLowerCase(),
  };
}

// A word of one character is a key ('g' finds ⌘G and ⇧⌘G, '?' the sheet's
// own key); a longer one may appear anywhere in the row.
function rowMatches(row, word) {
  if ([...word].length === 1) {
    return row.caps.has(word === '-' ? '−' : word);
  }

  return row.text.includes(word);
}

/**
 * The sheet's rows by group, in the registry's group order: every command
 * with keys, or with `customize` every command whose keys can change, with
 * or without keys. Each word of `query` must match: a one-character word a
 * key cap ('g', '?', '⌘'), a longer one the row's title, group, keywords,
 * where it works or its keys in words ('undo', 'cmd', 'shift').
 *
 * @param {object} [options]
 * @param {string} [options.query]
 * @param {boolean} [options.customize]
 * @param {string} [options.keep] a command id listed whatever the query (the
 *   one being recorded)
 * @param {'mac'|'other'} [options.platform]
 * @returns {{group: string, rows: object[]}[]}
 */
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

/**
 * The one-character shortcuts the single-key switch turns on and off, as
 * the platform labels them: ['?', '=', '+', '−', '⇧1'].
 *
 * @param {'mac'|'other'} [platform]
 * @returns {string[]}
 */
export function characterKeyLabels(platform = currentPlatform()) {
  const labels = COMMANDS.flatMap((command) =>
    commandKeys(command, { platform, all: true }),
  )
    .filter((key) => isCharacterKey(key))
    .map((key) => keyLabel(key, platform));

  return [...new Set(labels)];
}

/**
 * What the single-key switch turns on and off, for the hint beside it in
 * the shortcut sheet and the Settings dialog.
 *
 * @param {'mac'|'other'} [platform]
 * @returns {string}
 */
export function singleKeyHint(platform = currentPlatform()) {
  const labels = characterKeyLabels(platform);
  const mod = platform === 'mac' ? '⌘' : 'Ctrl';

  return labels.length
    ? `${listOf(labels)} work alone, without ${mod}. Turn them off if speech input or your screen reader might press them by mistake.`
    : 'No shortcut is a single key now.';
}

// --- recording -------------------------------------------------------------------

// On Windows and Linux the Windows (Super) key is the system's: Windows
// keeps nearly every shortcut with it, and Linux desktops most, so the page
// seldom sees them.
function systemKeyReason(spec, platform) {
  const parts = parseKey(spec);

  return parts?.meta && platform !== 'mac'
    ? `${keyLabel(spec, platform)} is not used: Windows and Linux keep most shortcuts with the Windows key for themselves.`
    : '';
}

// A command that works in text fields (the palette, Save now) cannot take a
// key that types a character there (typesCharacter: on macOS one pressed
// with ⌥ too): the field keeps such keys (see dispatchKeydown), so the
// shortcut would not work where the command promises to, and would block
// typing that character where it did.
function typedKeyReason(command, spec, platform) {
  if (
    !command ||
    !worksInTextFields(command) ||
    !typesCharacter(spec, platform)
  ) {
    return '';
  }

  const mods = platform === 'mac' ? '⌘ or ⌃' : 'Ctrl or Alt';

  return `${keyLabel(spec, platform)} types a character in text fields, where ${commandName(command)} works too, so its shortcut needs ${mods}.`;
}

/**
 * What giving a command a key would do. `status` is
 *   'refused'   the key cannot be a shortcut (`reason` says why): keymap's
 *               keyRefusal, a key that types a character for a command
 *               that works in text fields, or the Windows key on Windows
 *               and Linux
 *   'fixed'     a command whose keys cannot change has it (`fixed`)
 *   'conflict'  other commands have it where this one would work
 *               (`conflicts`); assignShortcut takes it only when told to
 *   'same'      the command has it already
 *   'free'      nothing else answers to it there
 * `inactive` is true for a one-character key while the single-key switch is
 * off: it is kept, but works only once they are on again. `others` counts
 * the command's other keys, which keeping this one replaces.
 *
 * @param {string|object} idOrCommand
 * @param {string} spec
 * @param {object} [options]
 * @param {'mac'|'other'} [options.platform]
 * @returns {{status: string, spec: string, label: string, spoken: string,
 *   reason: string, conflicts: object[], fixed: object[], inactive: boolean,
 *   others: number}}
 */
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
      keyRefusal(spec, platform) ||
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

/**
 * Makes a key a command's only shortcut. A key other commands have is
 * taken from them only with `take`; they keep their other keys.
 *
 * @param {string|object} idOrCommand
 * @param {string} spec
 * @param {object} [options]
 * @param {boolean} [options.take] take the key from the commands that have it
 * @param {'mac'|'other'} [options.platform]
 * @param {Storage|null} [options.storage] localStorage by default
 * @returns {{ok: boolean, verdict: object, taken: {id: string, name:
 *   string, keys: string[]}[]}} `taken` names the commands the key was taken
 *   from, with the keys they have left
 */
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

/**
 * Leaves a command without a shortcut (or at its defaults, when it has
 * none by default).
 *
 * @param {string|object} idOrCommand
 * @param {object} [options]
 * @param {'mac'|'other'} [options.platform]
 * @param {Storage|null} [options.storage]
 */
export function removeShortcut(
  idOrCommand,
  { platform = currentPlatform(), storage } = {},
) {
  const command = getCommand(idOrCommand);

  if (isCustomizable(command)) {
    storeKeys(command, [], platform, storage);
  }
}

/**
 * The keys as words, for what screen readers are told: 'Shift+Command+Z',
 * 'Ctrl+Shift+Z or Ctrl+Y', 'no shortcut'.
 *
 * @param {string[]} keys specs
 * @param {'mac'|'other'} [platform]
 * @returns {string}
 */
export function spokenKeys(keys, platform = currentPlatform()) {
  const words = keys.map((key) => spokenKey(key, platform));

  if (!words.length) {
    return 'no shortcut';
  }

  return words.length === 1
    ? words[0]
    : `${words.slice(0, -1).join(', ')} or ${words.at(-1)}`;
}

// Where a reason names a key: the first time its label stands as a word of
// its own, so the key L is not found in "Letters".
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

/**
 * What the recorder says about a verdict, as parts: plain text, and the
 * recorded key ({key: true}), which the sheet shows as key caps and screen
 * readers get as words. `tone` is 'info', 'warning' or 'error'.
 *
 * @param {object} verdict judgeShortcut's
 * @param {string} name the command's name (commandName)
 * @param {'mac'|'other'} [platform]
 * @returns {{tone: string, parts: ({text: string}|{key: true})[],
 *   spoken: string}}
 */
export function recorderMessage(verdict, name, platform = currentPlatform()) {
  const key = { key: true };
  let tone = 'info';
  let parts;

  switch (verdict.status) {
    case 'refused': {
      // A reason that names the key names it by its label, which becomes
      // the key part; one that does not is said after the key.
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
      parts.push({ text: ' Choose another.' });
      break;
    }
    case 'fixed':
      tone = 'error';
      parts = [
        key,
        {
          text: ` already runs ${listOf(verdict.fixed.map(commandName))}, which cannot change. Choose another.`,
        },
      ];
      break;
    case 'conflict':
      tone = 'warning';
      parts = [
        key,
        {
          text: ` already runs ${listOf(verdict.conflicts.map(commandName))}. Choose Use for ${name} to move it, or press another shortcut.`,
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
      text: ' Single-key shortcuts are off, so it works only once they are on.',
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
