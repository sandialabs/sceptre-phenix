// An experiment's files. Most requests encode the experiment's name; the
// list and read requests put it in as given.
import axiosInstance from '@/utils/axios.js';
import { useJobStore } from '@/utils/jobs.js';

const filesURL = (exp) => `experiments/${encodeURIComponent(exp)}/files`;

// GET /experiments/{name}/files; config carries params such as the sort
// column and the page, and the caller encodes exp if it should be
export function listExperimentFiles(exp, config) {
  return axiosInstance.get('experiments/' + exp + '/files', config);
}

// GET /experiments/{name}/files/{filename}?path=, the file name encoded
export function getExperimentFile(exp, file) {
  return axiosInstance.get(
    `experiments/${exp}/files/${encodeURIComponent(file.name)}`,
    {
      params: { path: file.path },
      headers: { Accept: 'text/plain' },
      // keep JSON files as text for fileText, which indents them,
      // rather than letting axios parse them
      responseType: 'text',
    },
  );
}

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

// POST /experiments/{name}/files/archive: the server builds the zip in a
// background job (see utils/jobs.js), which this returns
export function archiveExperimentFiles(exp, paths) {
  return useJobStore().start(
    axiosInstance.post(`${filesURL(exp)}/archive`, { paths }),
  );
}
