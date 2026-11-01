// Package partition reads the partition table of a disk image, an MBR (with its extended and logical partitions) or
// a GPT, and tells the file system of each partition from its magic bytes. It reads only through an [io.ReaderAt] of
// a known size and keeps every read inside that size. It reports a damaged table in Table.Notes, not as an error.
// Read gives the table, Probe the file system at an offset, and Inspect both. Partitions have the numbers Linux gives
// them, which `inject_partition` and minimega's `disk inject` use.
package partition
