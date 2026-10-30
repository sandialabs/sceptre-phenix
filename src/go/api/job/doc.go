// Package job runs the long operations the phenix UI server starts in the background, such as bulk VM actions, file
// archives and disk clones, so a client can follow, cancel and read the outcome of each after its request ends. Jobs
// live in memory only; docs/content/jobs.md describes their limits and lifecycle.
package job
