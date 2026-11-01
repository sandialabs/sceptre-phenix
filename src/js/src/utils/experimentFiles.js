// the selected paths still in the listed files, or the same array when all
// of them are
export function keepListed(selected, files) {
  const listed = new Set(files.map((f) => f.path));
  const kept = selected.filter((path) => listed.has(path));
  return kept.length == selected.length ? selected : kept;
}

// where one of the experiment's listed files downloads from, the token, when
// there is one, in the query
export function experimentFileURL(exp, file, token) {
  const query = new URLSearchParams({ path: file.path });
  if (token) query.set('token', token);
  return `${import.meta.env.BASE_URL}api/v1/experiments/${exp}/files/${encodeURIComponent(file.name)}?${query}`;
}
