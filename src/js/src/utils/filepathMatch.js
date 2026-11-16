// A port of Go's path/filepath.Match on Unix, which the server's RBAC uses: `*`
// and `?` never match a `/`. A malformed pattern matches nothing.

class BadPattern extends Error {}

// the code points of a string, so `?` and classes take one character
const chars = (s) => Array.from(s);

// splits off the leading stars and the run up to the next star outside a class
function scanChunk(pattern) {
  let star = false;
  while (pattern.length > 0 && pattern[0] === '*') {
    pattern = pattern.slice(1);
    star = true;
  }
  let inrange = false;
  let i;
  scan: for (i = 0; i < pattern.length; i++) {
    switch (pattern[i]) {
      case '\\':
        if (i + 1 < pattern.length) i++;
        break;
      case '[':
        inrange = true;
        break;
      case ']':
        inrange = false;
        break;
      case '*':
        if (!inrange) break scan;
    }
  }
  return [star, pattern.slice(0, i), pattern.slice(i)];
}

// one class endpoint, unescaped, and the rest of the chunk
function getEsc(chunk) {
  if (chunk.length === 0 || chunk[0] === '-' || chunk[0] === ']') {
    throw new BadPattern();
  }
  if (chunk[0] === '\\') {
    chunk = chunk.slice(1);
    if (chunk.length === 0) throw new BadPattern();
  }
  const rest = chunk.slice(1);
  if (rest.length === 0) throw new BadPattern();
  return [chunk[0], rest];
}

// the rest of s after the chunk matches its start, or null
function matchChunk(chunk, s) {
  // after a failure, the chunk is still read to check its syntax
  let failed = false;
  while (chunk.length > 0) {
    if (!failed && s.length === 0) failed = true;
    switch (chunk[0]) {
      case '[': {
        let r;
        if (!failed) {
          r = s[0];
          s = s.slice(1);
        }
        chunk = chunk.slice(1);
        let negated = false;
        if (chunk.length > 0 && chunk[0] === '^') {
          negated = true;
          chunk = chunk.slice(1);
        }
        let match = false;
        let nrange = 0;
        for (;;) {
          if (chunk.length > 0 && chunk[0] === ']' && nrange > 0) {
            chunk = chunk.slice(1);
            break;
          }
          let lo, hi;
          [lo, chunk] = getEsc(chunk);
          hi = lo;
          if (chunk[0] === '-') {
            [hi, chunk] = getEsc(chunk.slice(1));
          }
          const cp = r?.codePointAt(0);
          if (lo.codePointAt(0) <= cp && cp <= hi.codePointAt(0)) {
            match = true;
          }
          nrange++;
        }
        if (match === negated) failed = true;
        break;
      }
      case '?':
        if (!failed) {
          if (s[0] === '/') failed = true;
          s = s.slice(1);
        }
        chunk = chunk.slice(1);
        break;
      case '\\':
        chunk = chunk.slice(1);
        if (chunk.length === 0) throw new BadPattern();
      // falls through
      default:
        if (!failed) {
          if (chunk[0] !== s[0]) failed = true;
          s = s.slice(1);
        }
        chunk = chunk.slice(1);
    }
  }
  return failed ? null : s;
}

function match(pattern, name) {
  pattern: while (pattern.length > 0) {
    let star, chunk;
    [star, chunk, pattern] = scanChunk(pattern);
    if (star && chunk.length === 0) {
      // a trailing star matches the rest unless it holds a separator
      return !name.includes('/');
    }
    const t = matchChunk(chunk, name);
    // the last chunk must use up the name
    if (t !== null && (t.length === 0 || pattern.length > 0)) {
      name = t;
      continue;
    }
    if (star) {
      for (let i = 0; i < name.length && name[i] !== '/'; i++) {
        const t = matchChunk(chunk, name.slice(i + 1));
        if (t !== null) {
          if (pattern.length === 0 && t.length > 0) continue;
          name = t;
          continue pattern;
        }
      }
    }
    // a pattern that fails to match is still checked for bad syntax
    while (pattern.length > 0) {
      [, chunk, pattern] = scanChunk(pattern);
      matchChunk(chunk, []);
    }
    return false;
  }
  return name.length === 0;
}

export function filepathMatch(pattern, name) {
  try {
    return match(chars(pattern), chars(name));
  } catch (err) {
    if (err instanceof BadPattern) return false;
    throw err;
  }
}
