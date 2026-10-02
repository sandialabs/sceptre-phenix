// The Web Worker that matches an Auto-group pattern against the names (see
// planPatternGroups in grouping.js). It takes one message, {pattern, names},
// and answers {texts} (see matchTexts), or {error} with the engine's words
// when the pattern does not compile. The page ends it once it answers, or
// when it takes too long.

import { matchTexts } from './groupingPattern.js';

self.addEventListener('message', ({ data }) => {
  try {
    self.postMessage({ texts: matchTexts(data.pattern, data.names) });
  } catch (error) {
    self.postMessage({ error: String(error?.message || error) });
  }
});
