// Users and their API tokens.
import axiosInstance from '@/utils/axios.js';

// POST /users
export function createUser(user) {
  return axiosInstance.post('users', user);
}

// PATCH /users/{username}
export function updateUser(username, user) {
  return axiosInstance.patch(`users/${encodeURIComponent(username)}`, user);
}

// DELETE /users/{username}
export function deleteUser(username) {
  return axiosInstance.delete(`users/${encodeURIComponent(username)}`);
}

// POST /users/{username}/tokens, with the token's desc and lifetime
export function createUserToken(username, token) {
  return axiosInstance.post(
    `users/${encodeURIComponent(username)}/tokens`,
    token,
  );
}
