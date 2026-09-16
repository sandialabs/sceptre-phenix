import axios from 'axios';
import { usePhenixStore } from '@/store.js';
import { basePath } from '@/runtimeConfig.js';

const axiosInstance = axios.create({
  baseURL: `${basePath}api/v1/`,
});

axiosInstance.interceptors.request.use((config) => {
  const store = usePhenixStore();

  if (store.token) {
    config.headers.set('X-phenix-auth-token', 'bearer ' + store.token);
  }

  return config;
});

export default axiosInstance;
