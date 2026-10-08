// The server's size limits, mirroring phenix/api/builder limits.go and the
// limits of phenix/types/builder it takes.
//
// This module imports nothing, so the API client can read a limit without
// pulling the document decoder and validator into the chunk the rest of the
// UI loads.

// The largest document the server stores (MaxDocumentBytes in api/builder),
// and so the largest file the Builder reads.
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024;

// The longest name of a user, in UTF-8 bytes: one a document names as its
// author or last editor (MaxUserBytes in types/builder document.go), and one
// a draft is shared with (MaxOwnerLength in api/builder, which is that
// limit).
export const MAX_USER_BYTES = 256;

// The most people a draft, or a template or collection of the template
// library, is shared with (MaxShares in api/builder), used until the server
// says otherwise.
export const MAX_SHARES = 25;
