// Package diskchain follows the backing chain of a disk image as qemu does, reading only image headers and never
// running qemu-img, so phenix can find a missing backing file before minimega launches a VM. It follows qcow2
// (versions 2 and 3) and qcow version 1, and treats any other image as the base of its chain. It does not follow qed
// or vmdk images, external data files or backing files named by a protocol (nbd://, json:), and reports those as
// such. It also reports an image that qemu refuses: an unknown qcow2 version or feature bit, a corrupt mark, or
// encryption. Facts that do not stop qemu, such as an old version or a dirty bit, are notes of the walk.
package diskchain
