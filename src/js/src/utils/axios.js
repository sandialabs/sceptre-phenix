import axios from 'axios';
import { usePhenixStore } from '@/stores/phenix.js';

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

// axiosInstance.interceptors.request.use(
//   config => {
//     const store = usePhenixStore()
//
//     if (store.token) {
//       request.headers.set( 'X-phenix-auth-token', 'bearer ' + store.state.token)
//     }
//
//     return response => {
//       if (response.status == 401) {
//         store.logout()
//       }
//     }
//   }
// )
//
export default axiosInstance;
