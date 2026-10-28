// Node matchers: how the editor puts a device in a layer that no one chose for
// it (Layer.Matchers in document.go). The server checks patterns with RE2 and
// the editor runs them with RegExp, so the editor accepts only a pattern that
// both read the same way and that is quick to run. The document model imports
// this module, so this module imports nothing from it.

import { utf8Length } from './text.js';

// Bounds on the matchers of a layer (MaxLayerMatchers, MaxMatcherPathBytes
// and MaxMatcherPatternBytes in layerset.go).
export const MAX_LAYER_MATCHERS = 16;
export const MAX_MATCHER_PATH_BYTES = 128;
export const MAX_MATCHER_PATTERN_BYTES = 256;

// The most repeats with no largest count (*, + and {n,}) in a pattern
// (MaxMatcherRepeats in layerset.go).
export const MAX_MATCHER_REPEATS = 3;

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
 * Why a field path cannot be a matcher's, as the server would refuse it, or ''.
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

// Whether a repeat can repeat what it repeats more than once: *, +, {n,}
// and {n,m} with m above 1. ? and {0,1} cannot.
function repeatsMore(ch, repeat) {
  return repeat ? repeat.unbounded || repeat.most > 1 : ch !== '?';
}

// Why the pattern is not usable, or '': 'invalid', 'nested' (nestedRepeat in
// Go) or 'repeats' for too many *, + and {n,} (unboundedRepeats in Go).
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
  // Whether each open group has a repeat or a "|" in it, and whether the
  // last thing read is a group that has one.
  const mixed = [false];
  let mixedGroup = false;
  let nested = false;
  // The count of *, + and {n,} read.
  let unbounded = 0;

  // The last thing read is complete: no repeat comes after it.
  function settle() {
    groups[groups.length - 1] = Math.max(groups.at(-1), copies);
    copies = 0;
    mixedGroup = false;
  }

  for (let at = 0; at < pattern.length; ) {
    const ch = pattern[at];

    if (ch === '\\') {
      const length = escapeLength(pattern, at, inClass);

      if (!length) {
        return 'invalid';
      }

      if (inClass) {
        const ofClass = CLASS_ESCAPES.has(pattern[at + 1]);

        if (inRange && ofClass) {
          return 'invalid';
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
        return 'invalid';
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
        return 'invalid';
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
        return 'invalid';
      }

      settle();
      groups.push(0);
      mixed.push(false);
      at += 2 + (name ? name.length : 1);
      repeated = false;
      continue;
    }

    const repeat = ch === '{' ? repeatAt(pattern, at) : null;

    if (repeat || '*+?'.includes(ch)) {
      if (repeated || (repeat && repeat.most > MAX_REPEAT)) {
        return 'invalid';
      }

      if (mixedGroup && repeatsMore(ch, repeat)) {
        nested = true;
      }

      mixed[mixed.length - 1] = true;
      mixedGroup = false;

      if (repeat ? repeat.unbounded : ch !== '?') {
        unbounded += 1;
      }

      if (repeat) {
        copies = repeatCopies(repeat, copies);

        if (copies > MAX_REPEAT) {
          return 'invalid';
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
      mixed.push(false);
    } else if (ch === ')' && groups.length > 1) {
      // The group is the last thing read, which a repeat can repeat.
      copies = groups.pop();
      mixedGroup = mixed.pop();

      if (mixedGroup) {
        mixed[mixed.length - 1] = true;
      }
    } else if (ch === '|') {
      mixed[mixed.length - 1] = true;
    }

    repeated = false;
    at += 1;
  }

  if (nested) {
    return 'nested';
  }

  return unbounded > MAX_MATCHER_REPEATS ? 'repeats' : '';
}

/**
 * Why a pattern cannot be a matcher's, or '': empty, too long, read differently
 * by RE2 and RegExp, a nested repeat, or too many *, + and {n,} repeats.
 */
export function matcherPatternRule(pattern) {
  if (typeof pattern !== 'string' || pattern === '') {
    return 'required';
  }

  if (utf8Length(pattern) > MAX_MATCHER_PATTERN_BYTES) {
    return 'too-long';
  }

  return compiled(pattern).rule;
}

// Compiled patterns by their text, each as its RegExp and its rule (see
// matcherPatternRule). A pattern that is not usable has no RegExp.
const patterns = new Map();
const MAX_PATTERNS = 512;

// The RegExp of a pattern, compiled without flags as RE2 reads it, and its
// rule. The RegExp is null for a pattern that is not usable.
function compiled(pattern) {
  if (patterns.has(pattern)) {
    return patterns.get(pattern);
  }

  let rule = portable(pattern);
  let regex = null;

  if (rule !== 'invalid') {
    try {
      regex = new RegExp(pattern);
    } catch {
      rule = 'invalid';
    }
  }

  const result = { regex: rule ? null : regex, rule };

  if (patterns.size >= MAX_PATTERNS) {
    patterns.clear();
  }

  patterns.set(pattern, result);

  return result;
}

/**
 * The texts at a field path of a device spec. Text, numbers and booleans give
 * one, a list at the end of the path gives one per entry, and an object or a
 * missing field gives none.
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
 * The usable matchers of a list of layers, in the order that the conflict rule
 * tries them. Matchers with a bad path or pattern are left out.
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
            ? compiled(matchers[path]).regex
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
 * The first layer, in conflict rule order, with a matcher that the device
 * matches, and that matcher's path.
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
 * The layer that a device's fields match (see matchDevice). The answer is
 * cached while the layers and the payload stay the same objects.
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
