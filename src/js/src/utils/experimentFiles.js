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

// Files up to this size download through a request, which shows the server's
// error; larger ones, or of unknown size, through the browser's own download.
const BLOB_DOWNLOAD_MAX_BYTES = 256 * 1024 * 1024;

// whether a listed file downloads through a request rather than a link
export function downloadsAsBlob(file) {
  return (
    Number.isFinite(file.size) &&
    file.size >= 0 &&
    file.size <= BLOB_DOWNLOAD_MAX_BYTES
  );
}

// has the browser save url as name, in this tab, with its own progress
export function saveURL(url, name) {
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.hidden = true;
  document.body.append(link);
  link.click();
  link.remove();
}
