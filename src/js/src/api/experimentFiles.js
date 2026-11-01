// An experiment's files. The list and read requests put the experiment's name
// in the path as given.
import axiosInstance from '@/utils/axios.js';

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
