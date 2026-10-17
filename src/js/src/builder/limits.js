// The server's size limits. They mirror phenix/api/builder limits.go and the
// limits of phenix/types/builder that it uses.
//
// This module imports nothing. Thus the API client can read a limit without
// adding the document decoder and validator to the chunk that the rest of
// the UI loads.

// The largest document the server stores (MaxDocumentBytes in api/builder),
// and so the largest file the Builder reads.
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024;

// The longest user name, in UTF-8 bytes, that a document's metadata names
// as its creator or last editor (MaxUserBytes in types/builder document.go).
export const MAX_USER_BYTES = 256;
