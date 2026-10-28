// Configs, by their "kind/name" key, and the schemas they follow.
import axiosInstance from '@/utils/axios.js';

const JSON_BODY = 'application/json';

// POST /configs/download: one config as YAML, or several in a zip, as a Blob
export function downloadConfigs(keys) {
  return axiosInstance.post('configs/download', JSON.stringify(keys), {
    headers: {
      'Content-Type': JSON_BODY,
      Accept: 'application/x-yaml',
    },
    responseType: 'blob',
  });
}

// DELETE /configs/{kind}/{name}
export function deleteConfig(key) {
  return axiosInstance.delete('configs/' + key);
}

// POST /configs, with an uploaded file as FormData
export function uploadConfig(formData) {
  return axiosInstance.post('configs', formData);
}

// POST /configs
export function createConfig(config) {
  return axiosInstance.post('configs', JSON.stringify(config), {
    headers: { 'Content-Type': JSON_BODY },
  });
}

// PUT /configs/{kind}/{name}; with a revision, the server refuses the save
// (412) if the config has changed since it was loaded
export function updateConfig(key, config, revision) {
  const headers = { 'Content-Type': JSON_BODY };
  if (revision) headers['If-Match'] = `"r${revision}"`;
  return axiosInstance.put('configs/' + key, JSON.stringify(config), {
    headers,
  });
}

// GET /schemas/{version}: the schema of every kind at that version
export function getSchemaSpec(version) {
  return axiosInstance.get(`schemas/${version}`, {
    headers: { Accept: JSON_BODY },
  });
}

// GET /schemas/{kind}/{version}
export function getKindSchema(kind, version) {
  return axiosInstance.get(`schemas/${kind}/${version}`, {
    headers: { Accept: JSON_BODY },
  });
}
