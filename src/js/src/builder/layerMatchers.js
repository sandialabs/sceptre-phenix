// Node matchers: how the editor puts a device in a layer that no one chose
// for it (Layer.Matchers in document.go).
//
// A layer can have matchers, an object of field paths to regular
// expressions: {"general.hostname": "^plc-"}. A path is dotted keys into the
// device's spec, the object the Node Editor edits (general.hostname,
// hardware.os_type, network.interfaces.0.vlan). A key of digits indexes a
// list. The value at the path matches when the expression finds a match in
// it, as Go's MatchString finds one:
//
// - Text matches as it is. A number or a boolean matches as its text ("10",
//   "true").
// - A list, at the end of the path, matches when one of its entries
//   matches.
// - An object, null and a missing field never match.
//
// A layer matches a device when one of its matchers matches. When the
// matchers of more than one layer match, the match conflict rule of the
// diagram's layer set decides: the lowest layer ID (the default) or the
// highest. A layer the user chose for the device (its `layer`) always wins
// over a match (see effectiveNodeLayer in model.js). Matchers apply only to
// devices. A switch has no spec.
//
// The server checks the patterns with Go's regexp (RE2), and the editor
// applies them with JavaScript's RegExp. The two read most patterns the same
// way, but not all:
//
// - RE2 refuses what JavaScript alone accepts: lookarounds ((?=, (?!, (?<=,
//   (?<!), backreferences (\1, \k<name>), escapes such as \c and \u, a
//   class escape at the end of a range ([a-\d]), and repeats in repeats
//   that make more than 1000 copies ((a{500}){500}).
// - JavaScript refuses what RE2 alone accepts, such as flags ((?i)) and
//   (?P<name>).
// - Some patterns compile in both and mean different things: \A, \z, \pL,
//   \Q...\E, \x{41}, [[:alpha:]] and an empty class [].
//
// So the editor accepts only a pattern that both read the same way (see
// matcherPatternProblem). The server stays the authority: a pattern the
// editor accepts and RE2 refuses fails on save.
//
// This module imports nothing from the document model, which imports it.

import { utf8Length } from './text.js';

// Bounds on the matchers of a layer (MaxLayerMatchers, MaxMatcherPathBytes
// and MaxMatcherPatternBytes in layerset.go).
export const MAX_LAYER_MATCHERS = 16;
export const MAX_MATCHER_PATH_BYTES = 128;
export const MAX_MATCHER_PATTERN_BYTES = 256;

// A field path: dotted keys of letters, digits, "_" and "-"
// (matcherPathPattern in layerset.go).
export const MATCHER_PATH = /^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$/;

// The match conflict rules (MatchConflictLowest and MatchConflictHighest in
// layerset.go). No rule is the lowest.
export const MATCH_CONFLICT_LOWEST = 'lowest';
export const MATCH_CONFLICT_HIGHEST = 'highest';
export const MATCH_CONFLICTS = Object.freeze([
  MATCH_CONFLICT_LOWEST,
  MATCH_CONFLICT_HIGHEST,
]);

// The largest count RE2 takes in a repeat such as {2,5}.
const MAX_REPEAT = 1000;

// Escapes that RE2 and JavaScript read the same way: classes of
// characters, word boundaries, and control characters. \0 is NUL in both.
const SAME_ESCAPES = new Set([...'dDwWsSbBtnrfv0']);

// A key of digits, which indexes a list.
const INDEX = /^(0|[1-9][0-9]*)$/;

/**
 * Why a field path cannot be a matcher's, as the server would refuse it, or
 * '': empty, longer than MAX_MATCHER_PATH_BYTES, or not dotted keys.
 *
 * @param {*} path
 * @returns {'required'|'too-long'|'invalid'|''}
 */
export function matcherPathRule(path) {
  const text = typeof path === 'string' ? path : '';

  if (text === '') {
    return 'required';
  }

  if (utf8Length(text) > MAX_MATCHER_PATH_BYTES) {
    return 'too-long';
  }

  return MATCHER_PATH.test(text) ? '' : 'invalid';
}

// The text from `at` that a group opens with after "(?", when it is a name
// as both read it: "<name>". '' otherwise.
function groupName(pattern, at) {
  return /^<[A-Za-z_][A-Za-z0-9_]*>/.exec(pattern.slice(at))?.[0] || '';
}

// The repeat {n}, {n,} or {n,m} at `at`, as its length, its least and
// largest counts, and whether it has no largest count ({n,}). null when
// the brace starts no repeat (both then read it as a brace).
function repeatAt(pattern, at) {
  const found = /^\{(\d+)(,(\d*))?\}/.exec(pattern.slice(at));

  if (!found) {
    return null;
  }

  return {
    length: found[0].length,
    least: Number(found[1]),
    most: Math.max(Number(found[1]), Number(found[3] || 0)),
    unbounded: found[2] !== undefined && found[3] === '',
  };
}

// Whether the escape at `at` (a backslash) reads the same way in both. Its
// length, or 0 when it does not. In a class, \b is a backspace in
// JavaScript, and RE2 refuses it.
function escapeLength(pattern, at, inClass) {
  const next = pattern[at + 1];

  if (next === undefined || (inClass && (next === 'b' || next === 'B'))) {
    return 0;
  }

  if (SAME_ESCAPES.has(next)) {
    return 2;
  }

  if (next === 'x') {
    return /^[0-9A-Fa-f]{2}$/.test(pattern.slice(at + 2, at + 4)) ? 4 : 0;
  }

  // Punctuation stands for itself in both. A letter, a digit and any other
  // character name an escape that only one of them knows, or that they read
  // differently.
  return /^[!-/:-@[-`{-~]$/.test(next) ? 2 : 0;
}

// Escapes of a class of characters. JavaScript reads one at the end of a
// range ([a-\d]) as a character of the class, and RE2 refuses it.
const CLASS_ESCAPES = new Set([...'dDwWsS']);

// How many copies of the innermost thing a repeat makes, as RE2 counts them
// (repeatIsValid in Go's regexp/syntax): the counts of repeats inside repeats
// multiply. `inner` is that number for what the repeat repeats. {0} makes
// none, and a repeat with no largest count ({2,}) counts its least.
function repeatCopies(repeat, inner) {
  if (repeat.most === 0) {
    return 0;
  }

  const count = repeat.unbounded ? repeat.least : repeat.most;

  return count === 0 ? inner : count * Math.max(1, inner);
}

// Whether RE2 and JavaScript read the pattern the same way, apart from
// whether it compiles: no escape, group, class or repeat that only one of
// them knows or that they read differently.
function portable(pattern) {
  let inClass = false;
  // In a class: whether the last item read was one character, which a "-"
  // makes the start of a range, and whether a "-" opened a range.
  let rangeStart = false;
  let inRange = false;
  // Whether the last thing read was a repeat, which a second repeat would
  // repeat: RE2 refuses that.
  let repeated = false;
  // The copies (see repeatCopies) of each open group, the outermost first,
  // and of the last thing read, which a repeat after it repeats.
  const groups = [0];
  let copies = 0;

  // The last thing read is complete: no repeat comes after it.
  function settle() {
    groups[groups.length - 1] = Math.max(groups.at(-1), copies);
    copies = 0;
  }

  for (let at = 0; at < pattern.length; ) {
    const ch = pattern[at];

    if (ch === '\\') {
      const length = escapeLength(pattern, at, inClass);

      if (!length) {
        return false;
      }

      if (inClass) {
        const ofClass = CLASS_ESCAPES.has(pattern[at + 1]);

        if (inRange && ofClass) {
          return false;
        }

        rangeStart = !inRange && !ofClass;
        inRange = false;
      } else {
        settle();
      }

      at += length;
      repeated = false;
      continue;
    }

    if (inClass) {
      if (ch === '[' && pattern[at + 1] === ':') {
        return false;
      }

      if (ch === '-' && rangeStart && pattern[at + 1] !== ']') {
        inRange = true;
        rangeStart = false;
      } else {
        rangeStart = !inRange;
        inRange = false;
      }

      inClass = ch !== ']';
      at += 1;
      continue;
    }

    if (ch === '[') {
      const first = pattern[at + 1] === '^' ? at + 2 : at + 1;

      // [] and [^] are an empty and a full class in JavaScript, and RE2
      // reads the ] as a character of the class.
      if (pattern[first] === ']') {
        return false;
      }

      settle();
      inClass = true;
      rangeStart = false;
      inRange = false;
      repeated = false;
      at = first;
      continue;
    }

    if (ch === '(' && pattern[at + 1] === '?') {
      const name = groupName(pattern, at + 2);

      if (pattern[at + 2] !== ':' && !name) {
        return false;
      }

      settle();
      groups.push(0);
      at += 2 + (name ? name.length : 1);
      repeated = false;
      continue;
    }

    const repeat = ch === '{' ? repeatAt(pattern, at) : null;

    if (repeat || '*+?'.includes(ch)) {
      if (repeated || (repeat && repeat.most > MAX_REPEAT)) {
        return false;
      }

      if (repeat) {
        copies = repeatCopies(repeat, copies);

        if (copies > MAX_REPEAT) {
          return false;
        }
      }

      at += repeat ? repeat.length : 1;
      // A ? after a repeat makes it lazy, in both.
      if (pattern[at] === '?') {
        at += 1;
      }
      repeated = true;
      continue;
    }

    settle();

    if (ch === '(') {
      groups.push(0);
    } else if (ch === ')' && groups.length > 1) {
      // The group is the last thing read, which a repeat can repeat.
      copies = groups.pop();
    }

    repeated = false;
    at += 1;
  }

  return true;
}

/**
 * Why a pattern cannot be a matcher's, or '': empty (or not text), longer
 * than MAX_MATCHER_PATTERN_BYTES, or not a regular expression that the
 * server and the editor read the same way (see the top of this file).
 *
 * @param {*} pattern
 * @returns {'required'|'too-long'|'invalid'|''}
 */
export function matcherPatternRule(pattern) {
  if (typeof pattern !== 'string' || pattern === '') {
    return 'required';
  }

  if (utf8Length(pattern) > MAX_MATCHER_PATTERN_BYTES) {
    return 'too-long';
  }

  return compiled(pattern) ? '' : 'invalid';
}

// Compiled patterns by their text. A pattern that is not usable is null.
const patterns = new Map();
const MAX_PATTERNS = 512;

// The RegExp of a pattern, or null when it is not one that both read the
// same way. The pattern is compiled without flags, as RE2 reads it.
function compiled(pattern) {
  if (patterns.has(pattern)) {
    return patterns.get(pattern);
  }

  let regex = null;

  if (portable(pattern)) {
    try {
      regex = new RegExp(pattern);
    } catch {
      regex = null;
    }
  }

  if (patterns.size >= MAX_PATTERNS) {
    patterns.clear();
  }

  patterns.set(pattern, regex);

  return regex;
}

/**
 * The texts at a field path of a value: one for text, a number or a
 * boolean, one for each such entry of a list at the end of the path, and
 * none for an object, null or a missing field. A key of digits indexes a
 * list.
 *
 * @param {*} value the device's spec
 * @param {string[]} keys the path's keys
 * @returns {string[]}
 */
export function valuesAt(value, keys) {
  let current = value;

  for (const key of keys) {
    if (Array.isArray(current)) {
      if (!INDEX.test(key)) {
        return [];
      }

      current = current[Number(key)];
    } else if (current && typeof current === 'object') {
      if (!Object.hasOwn(current, key)) {
        return [];
      }

      current = current[key];
    } else {
      return [];
    }
  }

  const texts = (entry) => {
    if (typeof entry === 'string') {
      return [entry];
    }

    return (typeof entry === 'number' && Number.isFinite(entry)) ||
      typeof entry === 'boolean'
      ? [String(entry)]
      : [];
  };

  return Array.isArray(current) ? current.flatMap(texts) : texts(current);
}

// The texts a device has at a path: those of its spec. The device's
// hostname stands for general.hostname when the spec does not have one.
function deviceValues(device, keys) {
  const found = valuesAt(device?.spec, keys);

  if (
    found.length === 0 &&
    keys.length === 2 &&
    keys[0] === 'general' &&
    keys[1] === 'hostname' &&
    typeof device?.hostname === 'string' &&
    device.hostname !== ''
  ) {
    return [device.hostname];
  }

  return found;
}

/**
 * The matchers of a list of layers, ready to apply: the layers with usable
 * matchers, in the order the conflict rule tries them. A matcher whose path
 * or pattern is not usable is left out.
 *
 * @param {*} layers a document's `layers`
 * @param {string} [conflict] MATCH_CONFLICT_LOWEST (the default) or
 *   MATCH_CONFLICT_HIGHEST
 * @returns {{id: number, tests: {path: string, keys: string[], regex:
 *   RegExp}[]}[]}
 */
export function compileMatchers(layers, conflict = MATCH_CONFLICT_LOWEST) {
  const list = [];

  for (const layer of Array.isArray(layers) ? layers : []) {
    const matchers = layer?.matchers;

    if (
      !Number.isInteger(layer?.id) ||
      !matchers ||
      typeof matchers !== 'object' ||
      Array.isArray(matchers)
    ) {
      continue;
    }

    const tests = Object.keys(matchers)
      .sort()
      .filter((path) => !matcherPathRule(path))
      .map((path) => ({
        path,
        keys: path.split('.'),
        regex:
          typeof matchers[path] === 'string' &&
          !matcherPatternRule(matchers[path])
            ? compiled(matchers[path])
            : null,
      }))
      .filter((test) => test.regex);

    if (tests.length) {
      list.push({ id: layer.id, tests });
    }
  }

  return list.sort((a, b) =>
    conflict === MATCH_CONFLICT_HIGHEST ? b.id - a.id : a.id - b.id,
  );
}

/**
 * The layer a device's fields match (see compileMatchers): the first layer,
 * in the order of the conflict rule, that has a matcher that matches, and
 * the path of that matcher.
 *
 * @param {object} device a node's device payload
 * @param {ReturnType<typeof compileMatchers>} matchers
 * @returns {{id: number, path: string}|null}
 */
export function matchDevice(device, matchers) {
  for (const layer of matchers) {
    for (const test of layer.tests) {
      if (
        deviceValues(device, test.keys).some((text) => test.regex.test(text))
      ) {
        return { id: layer.id, path: test.path };
      }
    }
  }

  return null;
}

// The matchers of each list of layers by its conflict rule, and the match
// of each device payload. A document changes by new objects, so the same
// list and the same payload give the same answer, and typing in one field
// matches only the device it changes again.
const compiledLists = new WeakMap();

/**
 * The layer a device's fields match, with the matchers of a list of layers
 * and a conflict rule (see matchDevice). The answer is kept for the list
 * and the payload while both stay the same objects.
 *
 * @param {object} device a node's device payload
 * @param {*} layers a document's `layers`
 * @param {string} [conflict] see compileMatchers
 * @returns {{id: number, path: string}|null}
 */
export function matchedLayer(device, layers, conflict = MATCH_CONFLICT_LOWEST) {
  if (!Array.isArray(layers) || layers.length === 0 || !device) {
    return null;
  }

  const rule =
    conflict === MATCH_CONFLICT_HIGHEST
      ? MATCH_CONFLICT_HIGHEST
      : MATCH_CONFLICT_LOWEST;
  let byRule = compiledLists.get(layers);

  if (!byRule) {
    byRule = {};
    compiledLists.set(layers, byRule);
  }

  if (!byRule[rule]) {
    byRule[rule] = {
      matchers: compileMatchers(layers, rule),
      matches: new WeakMap(),
    };
  }

  const entry = byRule[rule];

  if (entry.matchers.length === 0) {
    return null;
  }

  if (typeof device !== 'object') {
    return matchDevice(device, entry.matchers);
  }

  if (!entry.matches.has(device)) {
    entry.matches.set(device, matchDevice(device, entry.matchers));
  }

  return entry.matches.get(device);
}
