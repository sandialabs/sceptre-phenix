package mm

import "strings"

type c2Executor func(...C2Option) (string, error)

// MountFilesystem issues a C2 command to mount a VM's filesystem, tolerating
// the case where it is already mounted.
func MountFilesystem(opts ...C2Option) error {
	return mountFilesystem(ExecC2Command, opts...)
}

func mountFilesystem(exec c2Executor, opts ...C2Option) error {
	opts = append(opts, C2Mount())

	_, err := exec(opts...)
	if err != nil && !strings.Contains(err.Error(), "already connected") {
		return err
	}

	return nil
}

// UnmountFilesystem issues a C2 command to unmount a VM's filesystem.
func UnmountFilesystem(opts ...C2Option) error {
	return unmountFilesystem(ExecC2Command, opts...)
}

func unmountFilesystem(exec c2Executor, opts ...C2Option) error {
	opts = append(opts, C2Unmount())

	_, err := exec(opts...)

	return err
}
