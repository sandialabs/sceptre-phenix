// Package freshness holds the ordering and freshness metadata phenix attaches to the data it serves (revisions,
// X-Phenix-Seq, X-Phenix-Observed-At and X-Phenix-Stale, described in docs/content/api.md) and its header helpers.
//
// # Server caches
//
// Every cache or reused result in the server is listed here with its bound, what invalidates it, and how a client
// learns its age. Add a new one here; one that serves minimega data reports its age with SetObserved.
//
//	name                    where                                  bound                  invalidated by                          reported as
//	----------------------  -------------------------------------  ---------------------  --------------------------------------  -------------------------------
//	cluster hosts           util/mm recentClusterHosts (2 kinds)   5 s (clusterHostsTTL)  a newer read; schedOnly always reads    X-Phenix-Observed-At (GET /hosts)
//	minimega VLAN side      api/vlan recent                        5 s (minimegaTTL)      a start or stop; phenix's VLAN changes  X-Phenix-Observed-At, minimega.read (GET /vlans)
//	host disk usage         util/mm diskUsageCache                 1 min (diskUsageTTL)   expiry                                  not reported (part of a host)
//	failed host disk usage  util/mm diskUsageCache                 15 s (diskUsageFailureTTL) expiry                              not reported (reads as 0)
//	headnode name           util/mm headnode                       5 min (headnodeTTL)    expiry                                  not data
//	experiment file lists   util/file experimentListings           5 s (listingTTL)       phenix's own file writes and deletes    X-Phenix-Observed-At (GET files)
//	SoH VM states           web lastVMStates                       while minimega is slow the next read; a stop                   vms_observed_at, vms_stale, X-Phenix-Stale
//	VM screenshots          web/util screenshot cache              14 s                   expiry                                  implicit (the image)
//	screenshot frames       web/broker frameCache                  screenshots in view    a changed image; leaving every view     implicit (the image)
//	topology search index   api/vm topologySearchKey               30 s, memory only      the experiment's store revision         exact
//	disk image details      api/disk local stamp cache             file stamps            a changed size, mtime or inode          exact
//	disk partitions         api/disk inspections                   file stamps, 1024      a changed file of the chain; refresh    exact
//	snapshot backing disks  util/mm snapshotDiskCache              one VM launch          the VM's relaunch                       exact
//	download checksums      web/util Digests                       file stamps            a changed file                          exact (Repr-Digest)
//	fixed-mode roles        web/middleware cachedRole              30 s (fixedRoleTTL)    expiry                                  not data
//	experiment locks        web/cache                              while held             unlock                                  not a data cache
//	UI assets               web assets                             the binary             a rebuild                               content-hash ETag
//	served openapi.yml      web OpenAPISpecHandler                 file stamps            a changed spec file                     content-hash ETag
//	idempotent job starts   web/idempotency Store                  1 h, 1000, 16 MiB      expiry; the oldest dropped when full    Idempotent-Replayed: true
//	app runtime details     app stageRuntime                       one ApplyApps run      the next run                            not data (app input)
//	minimega version        util/mm minimegaVersion                the process            a restart                               not reported (run info, captures)
//	VLAN blacklist removal  util/mm blacklistRemoval               until a redial         mmcli.Redials changes; ForgetCluster    not reported (a stale true fails the removal)
//
// The experiment file lists also hold the capture format of each headnode file, read from its first bytes once for
// each listing.
//
// Concurrent identical minimega reads (VM info, SoH VM states, cluster hosts) share one run
// (util/flight Group) only if it has not started, so a shared answer is never older than the call. The full lists of
// cluster hosts with and without disk usage are read and kept apart. The schedOnly reads of the cluster hosts
// (mm.GetClusterHostsObserved, for the schedulers, apps and Builder preflight) share nothing, so their own deadlines
// bound them.
package freshness
