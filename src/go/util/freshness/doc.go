// Package freshness holds the ordering and freshness metadata phenix attaches
// to the data it serves: config revisions, which order writes to one config
// across processes, and the helpers that put them in HTTP headers.
//
// # Revisions
//
// Every write to a store config gives it a new Metadata.Revision, taken from a
// counter in BoltDB or from etcd's ModRevision. A revision is opaque and only
// increases; compare it for equality. The REST API exposes it as a strong
// ETag, "r<revision>", and honors If-Match on writes.
package freshness
