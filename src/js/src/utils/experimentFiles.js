import FileSaver from 'file-saver';
import axiosInstance from '@/utils/axios.js';

const filesURL = (exp) => `experiments/${encodeURIComponent(exp)}/files`;

// DELETE /experiments/{name}/files/{filename}?path=
export function deleteExperimentFile(exp, file) {
  return axiosInstance.delete(
    `${filesURL(exp)}/${encodeURIComponent(file.name)}`,
    { params: { path: file.path } },
  );
}

// POST /experiments/{name}/files/delete: the server deletes what it can and
// returns { deleted: [path], failed: [{ path, error }] }
export async function deleteExperimentFiles(exp, paths) {
  const resp = await axiosInstance.post(`${filesURL(exp)}/delete`, { paths });
  return { deleted: resp.data?.deleted ?? [], failed: resp.data?.failed ?? [] };
}

// POST /experiments/{name}/files/download: saves the zip the server streams
export async function downloadExperimentFiles(exp, paths) {
  try {
    const resp = await axiosInstance.post(
      `${filesURL(exp)}/download`,
      { paths },
      { responseType: 'blob' },
    );
    FileSaver.saveAs(resp.data, `${exp}-files.zip`);
  } catch (err) {
    // with responseType blob the error text arrives as a Blob too
    if (err?.response?.data instanceof Blob) {
      err.response.data = await err.response.data.text();
    }
    throw err;
  }
}

// the selected paths still in the listed files, or the same array when all
// of them are
export function keepListed(selected, files) {
  const listed = new Set(files.map((f) => f.path));
  const kept = selected.filter((path) => listed.has(path));
  return kept.length == selected.length ? selected : kept;
}
