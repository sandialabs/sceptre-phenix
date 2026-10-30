// Package disk lists the images in the minimega files directory, those experiment topologies name and the images
// backing them, and uploads, renames, copies, deletes, rebases, snapshots and commits them through minimega (see
// docs/content/disks.md for what the list leaves out and the actions refuse). In a mesh every disk is on the headnode:
// minimega copies those in its files directory to other nodes at launch, and none outside it.
package disk
