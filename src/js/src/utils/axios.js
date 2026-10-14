import axios from 'axios';
import { usePhenixStore } from '@/store.js';
import { readFreshness } from '@/utils/freshness.js';

const axiosInstance = axios.create({
  // BASE_URL is the app's base path, slash-terminated (see vite.config.js)
  baseURL: `${import.meta.env.BASE_URL}api/v1/`,
});

axiosInstance.interceptors.request.use((config) => {
  const store = usePhenixStore();

  if (store.token) {
    config.headers.set('X-phenix-auth-token', 'bearer ' + store.token);
  }

  // a request given a FreshnessMeta (config.meta) reports its response's
  // freshness into it; see freshness.js
  if (config.meta) config.metaSentAt = Date.now();

  return config;
});

axiosInstance.interceptors.response.use((response) => {
  const { meta, metaSentAt } = response.config ?? {};
  if (meta) readFreshness(meta, response.headers, metaSentAt, Date.now());
  return response;
});

export default axiosInstance;
