// Signing in, signing up and signing out.
import axiosInstance from '@/utils/axios.js';

// GET /login: who the proxy in front of phēnix says the user is
export function getProxyLogin() {
  return axiosInstance.get('login');
}

// POST /login
export function logIn(user, pass) {
  return axiosInstance.post('login', { user, pass });
}

// POST /signup, with the new user's username, names and, unless a proxy
// signs users in, password
export function signUp(user) {
  return axiosInstance.post('signup', user);
}

// POST /logout
export function logOut() {
  return axiosInstance.post('logout');
}
