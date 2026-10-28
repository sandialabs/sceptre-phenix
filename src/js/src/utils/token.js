// The expiry of the session token, which the router and the Builder read.

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

/**
 * Whether the token says it has expired. A token that is not a JWT (without
 * authentication) never expires.
 */
export function tokenExpired(token) {
  return Date.now() >= (tokenExpiry(token) ?? Infinity);
}
