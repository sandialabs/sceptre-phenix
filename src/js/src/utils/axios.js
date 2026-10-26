import axios from 'axios';
import { usePhenixStore } from '@/store.js';
import {
  readFreshness,
  serverResponded,
  serverUnanswered,
  serverUnavailable,
} from '@/utils/freshness.js';
import { serverNotAnswering } from '@/utils/unreachable.js';

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
  config.sentAt = Date.now();

  return config;
});

const retryAfterHeader = (headers) =>
  (typeof headers?.get === 'function'
    ? headers.get('retry-after')
    : headers?.['retry-after']) ?? undefined;

axiosInstance.interceptors.response.use(
  (response) => {
    const { meta, metaSentAt, sentAt } = response.config ?? {};
    serverResponded(sentAt);
    if (meta) readFreshness(meta, response.headers, metaSentAt, Date.now());
    return response;
  },
  (error) => {
    const response = error?.response;
    // a server shutting down answers 503, and a proxy 502 or 504 for one
    // that is down; any other answer means it is serving
    if (serverNotAnswering(error)) {
      serverUnanswered();
    } else if (response?.status === 503) {
      const retryAfter = retryAfterHeader(response.headers);
      if (retryAfter !== undefined) serverUnavailable(retryAfter);
    } else if (response) {
      serverResponded(error.config?.sentAt);
    }
    return Promise.reject(error);
  },
);

export default axiosInstance;
