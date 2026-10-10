// The server's size limits. They mirror phenix/api/builder limits.go and the
// limits of phenix/types/builder that it uses.
//
// This module imports nothing. Thus the API client can read a limit without
// adding the document decoder and validator to the chunk that the rest of
// the UI loads.

// The largest document the server stores (MaxDocumentBytes in api/builder),
// and so the largest file the Builder reads.
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024;

// The longest user name, in UTF-8 bytes. This applies to a user that a
// document's metadata names as its creator or last editor (MaxUserBytes in
// types/builder document.go). It also applies to a user that a draft is
// shared with (MaxOwnerLength in api/builder, which is that limit).
export const MAX_USER_BYTES = 256;

// The maximum number of people that a draft, or a template or collection of
// the template library, is shared with (MaxShares in api/builder). The UI
// uses this value until the server gives a different one.
export const MAX_SHARES = 25;
