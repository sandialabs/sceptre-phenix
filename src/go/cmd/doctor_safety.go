package cmd

import (
	"fmt"
)

// linuxFilesystems names the filesystem types that statfs gives on Linux, by magic number (linux/magic.h). Only
// the network and FUSE filesystems matter to store-fs.
//
//nolint:gochecknoglobals // constant table
var linuxFilesystems = map[uint32]string{
	0x6969:     "nfs",
	0x517b:     "smb",
	0xff534d42: "cifs",
	0xfe534d42: "smb2",
	0x65735546: "fuse",
	0x00c36400: "ceph",
	0x01021997: "9p",
	0x0bd00bd0: "lustre",
	0x47504653: "gpfs",
	0x01161970: "gfs2",
	0x6b414653: "afs",
	0xef53:     "ext4",
	0x58465342: "xfs",
	0x9123683e: "btrfs",
	0x2fc12fc1: "zfs",
	0x01021994: "tmpfs",
	0x794c7630: "overlayfs",
}

// linuxFilesystemName names the Linux filesystem type of magic, or gives its number.
func linuxFilesystemName(magic uint32) string {
	if name, ok := linuxFilesystems[magic]; ok {
		return name
	}

	return fmt.Sprintf("type %#x", magic)
}
