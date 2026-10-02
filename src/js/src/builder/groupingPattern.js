// Auto-group by name pattern: checking the pattern the user typed, and
// matching it against the names (see grouping.js).
//
// The pattern is a JavaScript regular expression, compiled to ignore case.
// A regular expression cannot be stopped once it runs, and some take
// exponential time on a short name, so the matching runs in a Web Worker
// the page can end (groupingWorker.js). This module is what the worker
// loads, so it imports nothing.

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
 * The text each name is grouped by: what the first pair of parentheses
 * matched when the pattern has one and it took part in the match, the
 * whole match otherwise, trimmed.
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
