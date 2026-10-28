/*
Package disk is an implementation of the phenix disk API.
This API is used for managing existing images/disks used by VMs.

Provides functionality for getting a detailed list of disks.
Allows for basic operations such as uploading, deleting, renaming, and copying.
Also allows for QEMU operations such as rebasing, snapshotting, and committing by wrapping minimega commands.

The list holds the images in the minimega files directory and its folders
(down to maxDepth levels, without following symlinked folders), the images
experiment topologies name wherever they are, and the images backing any of
them. An image is identified by its cleaned absolute path. The listing leaves
out the folders experiments and minimega keep their own files in (see
skipped), and the actions refuse images outside the files directory or in
those folders (see Resolve).

NOTE: In a mesh, it is assumed that all disks are on the head node.
minimega handles copying disks in its files directory to other nodes at launch,
and expects disks on the head. It does not copy images outside that directory.
*/
package disk
