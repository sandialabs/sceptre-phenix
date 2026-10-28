// Auto-group by name pattern: checks the typed pattern and matches it against
// the names. A regular expression cannot stop once it starts, so the matching
// runs in a Web Worker that the page can stop (groupingWorker.js). The worker
// loads this module, so it imports nothing.

export const GROUP_PATTERN_MAX = 200;

// The part of a name a pattern is run on, in characters from its start.
export const GROUP_NAME_MAX = 255;

/**
 * Why a pattern cannot be used, in words, or '' for a pattern that compiles.
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
 * The trimmed text by which each name is grouped: the first matched pair of
 * parentheses, else the whole match. null when the pattern matches no text.
 */
export function matchTexts(pattern, names) {
  const regex = new RegExp(pattern, 'i');

  return names.map((name) => {
    const match = regex.exec(String(name ?? '').slice(0, GROUP_NAME_MAX));
    const text = match ? (match[1] ?? match[0]).trim() : '';

    return text || null;
  });
}
