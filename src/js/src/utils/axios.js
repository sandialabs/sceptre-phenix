import axios from 'axios';
import { usePhenixStore } from '@/store.js';

const axiosInstance = axios.create({
  // BASE_URL is the app's base path, slash-terminated (see vite.config.js)
  baseURL: `${import.meta.env.BASE_URL}api/v1/`,
});

axiosInstance.interceptors.request.use((config) => {
  const store = usePhenixStore();

  if (store.token) {
    config.headers.set('X-phenix-auth-token', 'bearer ' + store.token);
  }

  return config;
});

export default axiosInstance;
