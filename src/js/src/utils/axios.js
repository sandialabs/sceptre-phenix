import axios from 'axios';
import { usePhenixStore } from '@/store.js';

const axiosInstance = axios.create({
  baseURL: `/api/v1/`,
});

axiosInstance.interceptors.request.use((config) => {
  const store = usePhenixStore();

  if (store.token) {
    config.headers.set('X-phenix-auth-token', 'bearer ' + store.token);
  }

  return config;
});

export default axiosInstance;
