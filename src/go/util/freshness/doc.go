// Package freshness holds the ordering and freshness metadata phenix attaches
// to the data it serves, and the helpers that put it in HTTP headers.
//
// # Revisions
//
// Every write to a store config gives it a new Metadata.Revision, taken from a
// counter in BoltDB. A revision is opaque and only increases; compare it for
// equality. It orders writes to one config across processes. The REST API
// exposes it as a strong ETag, "r<revision>", and honors If-Match on writes.
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
//
// # Server caches
//
// Every cache or reused result in the server is listed here with its bound,
// what invalidates it, and how a client learns its age. A new one must be
// added here, and one that serves minimega data must report its age with
// SetObserved.
//
//	name                    where                                  bound                  invalidated by                          reported as
//	----------------------  -------------------------------------  ---------------------  --------------------------------------  -------------------------------
//	cluster hosts           util/mm recentClusterHosts             5 s (clusterHostsTTL)  a newer read; schedulers always read    X-Phenix-Observed-At (GET /hosts)
//	host disk usage         util/mm diskUsageCache                 1 min (diskUsageTTL)   expiry                                  not reported (part of a host)
//	headnode name           util/mm headnode                       5 min (headnodeTTL)    expiry                                  not data
//	experiment file lists   util/file experimentListings           5 s (listingTTL)       phenix's own file writes and deletes    X-Phenix-Observed-At (GET files)
//	SoH VM states           web lastVMStates                       while minimega is busy the next read                           vms_observed_at, vms_stale, X-Phenix-Stale
//	VM screenshots          web/util screenshot cache              14 s                   expiry                                  implicit (the image)
//	screenshot frames       web/broker frameCache                  screenshots in view    a changed image; leaving every view     implicit (the image)
//	topology search index   api/vm topologySearchKey               30 s, memory only      the experiment's store revision         exact
//	disk image details      api/disk local stamp cache             file stamps            a changed size, mtime or inode          exact
//	snapshot backing disks  util/mm snapshotDiskCache              one VM launch          the VM's relaunch                       exact
//	download checksums      web/util Digests                       file stamps            a changed file                          exact (Repr-Digest)
//	fixed-mode roles        web/middleware cachedRole              30 s (fixedRoleTTL)    expiry                                  not data
//	experiment locks        web/cache                              while held             unlock                                  not a data cache
//	UI assets               web assets                             the binary             a rebuild                               content-hash ETag
//	served openapi.yml      web OpenAPISpecHandler                 file stamps            a changed spec file                     content-hash ETag
//	idempotent job starts   web/idempotency Store                  1 h, 1000, 16 MiB      expiry; the oldest dropped when full    Idempotent-Replayed: true
//
// Concurrent identical minimega reads (VM info, SoH VM states, cluster hosts) share one run
// (util/flight Group) only if it has not started, so a shared answer is never older than the call.
package freshness
