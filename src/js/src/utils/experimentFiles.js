// the selected paths still in the listed files, or the same array when all
// of them are
export function keepListed(selected, files) {
  const listed = new Set(files.map((f) => f.path));
  const kept = selected.filter((path) => listed.has(path));
  return kept.length == selected.length ? selected : kept;
}
