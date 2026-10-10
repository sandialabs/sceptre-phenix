// Auto-group by name pattern: checks the pattern that the user typed, and
// matches it against the names (see grouping.js).
//
// The pattern is a JavaScript regular expression, compiled case-insensitive.
// A regular expression cannot stop after it starts, and some take
// exponential time on a short name. Thus the matching runs in a Web Worker
// that the page can stop (groupingWorker.js). The worker loads this module,
// so the module imports nothing.

export const GROUP_PATTERN_MAX = 200;

// The part of a name a pattern is run on, in characters from its start.
export const GROUP_NAME_MAX = 255;

/**
 * Why a pattern cannot be used, in words.
 *
 * @param {string} pattern
 * @returns {string} '' for a pattern that compiles
 */
export function patternProblem(pattern) {
  const text = typeof pattern === 'string' ? pattern : '';

  if (text.length === 0) {
    return 'Enter a pattern.';
  }

  if (text.length > GROUP_PATTERN_MAX) {
    return `The pattern can have at most ${GROUP_PATTERN_MAX} characters.`;
  }

  try {
    new RegExp(text, 'i');
  } catch (error) {
    return `That is not a valid regular expression. ${error.message}`;
  }

  return '';
}

/**
 * The text by which each name is grouped, trimmed. When the pattern has a
 * first pair of parentheses that took part in the match, it is the text that
 * the pair matched. Otherwise it is the whole match.
 *
 * @param {string} pattern one patternProblem passes
 * @param {string[]} names
 * @returns {(string|null)[]} for each name its text, or null when the
 *   pattern does not match it or matches no text
 */
export function matchTexts(pattern, names) {
  const regex = new RegExp(pattern, 'i');

  return names.map((name) => {
    const match = regex.exec(String(name ?? '').slice(0, GROUP_NAME_MAX));
    const text = match ? (match[1] ?? match[0]).trim() : '';

    return text || null;
  });
}
