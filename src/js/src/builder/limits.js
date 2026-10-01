// The server's size limits, mirroring phenix/api/builder limits.go.
//
// This module imports nothing, so the API client can read a limit without
// pulling the document decoder and validator into the chunk the rest of the
// UI loads.

// The largest document the server stores (MaxDocumentBytes in api/builder),
// and so the largest file the Builder reads.
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024;
