/*
Package job keeps the long-running operations the phenix UI server runs in the
background, such as bulk VM actions, experiment file archives and disk clones,
so a client can follow them after its request ends, cancel them, and read each
item's outcome.

A job runs a Spec's Run function on its own goroutine, which reports each
item's outcome, byte progress and any file the job produces (its artifact)
through the Run it is handed. The Registry holds the jobs, starts at most a
few at once for each owner and in all (the rest wait, queued, in the order they
came), tells a notifier of every change (progress at most every
NotifyInterval, status changes at once), and forgets a finished job, deleting
its artifact, an hour after it finished or once more than 200 finished jobs are
kept.

Jobs live in memory only: a restart of the server loses them, and stops the
work of the running ones. Callers clean up what such work leaves behind, such
as partial files, when the server starts (see RemoveStaleArtifacts).
*/
package job
