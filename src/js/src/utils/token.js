/** The expiry of a JWT in milliseconds since the epoch, or null. */
export function tokenExpiry(token) {
  try {
    const claims = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    const { exp } = JSON.parse(atob(claims));

    return Number.isFinite(exp) ? exp * 1000 : null;
  } catch {
    return null;
  }
}
