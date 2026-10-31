// The server's size limits. They mirror limits.go in phenix/api/builder.
// This module imports nothing, so the API client does not load the validator.

// The largest document the server stores (MaxDocumentBytes in api/builder),
// and so the largest file the Builder reads.
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024;

// The longest user name, in UTF-8 bytes, that a document's metadata names
// as its creator or last editor (MaxUserBytes in types/builder document.go).
export const MAX_USER_BYTES = 256;

// The most drafts the server holds (MaxDrafts in api/builder). A new draft
// past it is refused with the code draft.limit.
export const MAX_DRAFTS = 2000;
