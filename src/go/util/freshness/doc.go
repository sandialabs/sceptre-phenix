// Package freshness holds the ordering and freshness metadata phenix attaches
// to the data it serves, and the helpers that put it in HTTP headers.
//
// # Revisions
//
// Every write to a store config gives it a new Metadata.Revision, taken from a
// counter in BoltDB or from etcd's ModRevision. A revision is opaque and only
// increases; compare it for equality. It orders writes to one config across
// processes. The REST API exposes it as a strong ETag, "r<revision>", and
// honors If-Match on writes.
//
// # Sequence
//
// Seq orders everything one phenix process tells its clients: every REST
// response carries the seq taken as its request started (X-Phenix-Seq), and
// every websocket message the seq taken as it was published. Data tagged S
// reflects every change whose message carries a seq of S or less. Unlike a
// revision, a seq is comparable only within one process.
//
// # Observations
//
// Runtime state read from minimega is reported with when it was read
// (X-Phenix-Observed-At), whether it is older than its normal bound
// (X-Phenix-Stale), and whether it is known at all: a read that failed is
// never reported as an empty success.
package freshness
