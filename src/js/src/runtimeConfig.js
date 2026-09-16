const validAuthModes = ['disabled', 'enabled', 'proxy'];

// These settings only control client routing and URLs. The Go HTTP middleware
// independently enforces authentication for every protected API request.
function metaContent(name) {
  if (typeof document === 'undefined') {
    return '';
  }

  return document.querySelector(`meta[name="${name}"]`)?.content || '';
}

export function normalizeBasePath(value) {
  const path = value || '/';
  const withLeadingSlash = path.startsWith('/') ? path : `/${path}`;

  return withLeadingSlash.endsWith('/')
    ? withLeadingSlash
    : `${withLeadingSlash}/`;
}

export const basePath = normalizeBasePath(
  metaContent('phenix-base-path') || import.meta.env.BASE_URL,
);

export const authMode =
  metaContent('phenix-auth-mode') || import.meta.env.VITE_AUTH || 'disabled';

if (!validAuthModes.includes(authMode)) {
  throw new Error(`invalid phenix UI authentication mode: ${authMode}`);
}
