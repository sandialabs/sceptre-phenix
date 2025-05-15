/*
Implementation of the phenix disk API. 
This API is used for managing existing images/disks used by VMs.

Provides functionality for getting a detailed list of disks.
Allows for basic operations such as uploading, deleting, renaming, and copying.
Also allows for QEMU operations such as rebasing, snapshotting, and committing by wrapping minimega commands.

NOTE: In a mesh, it is assumed that all disks are on the head node. 
minimega handles copying disks to other nodes at launch, and expects disks on the head.
*/
package disk