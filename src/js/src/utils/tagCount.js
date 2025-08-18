export function tagCount(tags) {
  return Object.keys(tags).filter(
    (entry) => !entry.startsWith('__') || entry.startsWith('__notes_'),
  ).length;
}
