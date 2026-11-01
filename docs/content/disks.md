# Disks

`GET /api/v1/disks` lists the disk images phēnix knows about:

* the images with a known extension (`.qcow2`, `.qc2`, `.qcow`, `_rootfs.tgz`,
  `.hdd`, `.iso`) in the minimega files directory and the folders under it,
  except the folders described in [Images in Folders](#images-in-folders);
* every image an experiment's topology names, wherever it is and whatever its
  extension, as long as phēnix can open it as a regular file;
* the images those are backed by.

Each image is listed once, by its full path, so two images with the same file
name in different folders are two entries. The list needs `list` on `disks`,
and only images your role may see appear; see [Permissions](#permissions).

The same images can be managed from the command line; see
[Virtual Disk Images Management](image.md).

## In Use

An image counts as in use when a process holds a lock on its file, which is how
minimega decides the same thing. In practice that means a running VM has the
file open.

Each image in a backing chain is judged on its own file. A backing image is not
marked in use just because an image built on it is, so a base image stays
available for snapshots while VMs run off its children.

## Experiments Using an Image

An experiment uses an image when a node in its topology names the image as a
drive, or when the image backs a drive the topology names. phēnix matches them
by full path, so a topology naming `win/base.qc2` does not use `base.qc2` at the
top of the minimega files directory. The list gives, for each image, every
experiment using it that your role may list.

## Images in Folders

Images can sit in folders under the minimega files directory, and a topology
names them by their path within it (`image: win/10/win10.qcow2`) or by their
full path. The list searches folders down to 16 levels below the files
directory, and leaves out:

* hidden files and folders, whose names start with `.`, and `lost+found`;
* a `files`, `tmp` or `miniccc_responses` folder directly inside a top-level
  folder, such as `my-experiment/files`: phēnix keeps an experiment's files,
  scratch images and command responses there;
* anything at the top level named `saved` or starting with `transfer_`, where
  minimega keeps saved VM state and files it is copying between cluster nodes.

Symlinked folders are not followed, so an image in one is listed only when a
topology names it; the files directory itself may be a symlink. A symlink to an
image file is listed at the link's path. A folder phēnix cannot read is left out
and logged, and the first folder found too deep is logged as
`disk list leaves out folders nested too deeply`.

An image the list leaves out is still listed when a topology names it, such as
a VM disk snapshot restored from an experiment's `files` folder, but phēnix does
not act on it (see [Actions](#actions)).

## Images Outside the Files Directory

A topology can also name an image anywhere else phēnix can read it, such as
`/data/vms/win10.qcow2`. The list shows such an image by its full path, with
`outsideFilesDir` set.

minimega copies only images in its files directory to the cluster nodes that
launch VMs from them. Before using an outside image on a cluster of more than
one node, put the same file at the same path on every node that may run the VM.

!!! warning
    With minimega's `-abssnapshot` flag, which the Docker Compose setup turns
    on with `MM_ABSSNAPSHOT: true`, minimega cannot launch a snapshot VM whose
    image is outside its files directory on a cluster of more than one node,
    even when every node has the file. Keep such images in the files directory.

phēnix lists outside images but does not change them: the server refuses every
action on them, `Download` included. To manage one, copy it into the minimega
files directory and point the topology at the copy.

phēnix reads an image from the path the topology names, and minimega must find
it at that same path. In the Docker Compose setup the phēnix and minimega
containers share `/phenix`, so an image elsewhere under `/phenix` works as it
is; a path outside `/phenix` needs the same mount in both containers.

phēnix leaves out a topology image it cannot open as a regular file, such as a
missing or unreadable one, and never inspects paths under `/proc`, `/sys`,
`/dev` or `/run`.

## Permissions

`disks` permissions name an image by its file name, such as `win10.qcow2`, in
whatever folder it is, so a name covers the files of that name in every folder:
`list` on `win10.qcow2` lists both `win10.qcow2` and `win/win10.qcow2`.

A role sees an image outside the minimega files directory only when, besides
`list` on its file name, it may `get` an experiment whose topology uses the
image, or the image backs one the role sees. `GET /api/v1/disks?expName=` asks
for the images one experiment can use, which needs `get` on that experiment.

## Actions

The server offers `Snapshot`, `Commit`, `Rebase`, `Clone`, `Resize`,
`Download`, `Rename`, and `Delete`. It refuses an action on an image outside
the files directory or at a path the list leaves out. It also refuses to
snapshot, commit, rebase, resize, rename, or delete an image that is in use.
`Clone` and `Download` still work on an image in use, because they only read
the file. `Snapshot`, `Commit`, and `Rebase` apply to VM images only.

Each action needs a permission on `disks`: `create` for `Snapshot` and
`Clone`, which the server checks against the new image's file name, along with
`get` on the image they copy, `update` for `Rebase`, `Rename`, and `Resize`,
`update` on both the image and its *backing* image for `Commit`, `get` for
`Download`, and `delete` for `Delete`.

`Snapshot`, `Clone`, and `Rename` take a new name, which is relative to the
image's own folder unless it is a full path in the minimega files directory.
A snapshot is always a qcow2 image, so phēnix adds `.qcow2` to a snapshot name
unless it ends in `.qc2` or `.qcow2`. `Clone` and `Rename` do not change the
format of the image, so the extension of the image decides the new name:

| Image extension | The new name can end in | Extension that phēnix adds |
|---|---|---|
| `.qcow2` or `.qc2` | `.qcow2` or `.qc2` | `.qcow2` |
| `.qcow` | `.qcow`, `.qcow2` or `.qc2` | `.qcow` |
| `.iso` | `.iso` | `.iso` |
| `.hdd` | `.hdd` | `.hdd` |
| `_rootfs.tgz` | `_rootfs.tgz` | `_rootfs.tgz` |

A new name that ends in an extension of the second column stays as it is.
phēnix refuses a new name that ends in a different known image extension, such
as `bar.qcow2` for `foo.iso`, and the error gives the extensions that the name
can have. phēnix adds the extension of the third column to all other names, so
a clone named `bar` of `foo.iso` is `bar.iso`. For an image with no known
extension, phēnix refuses no name: it adds `.qcow2` unless the name ends in
`.qc2` or `.qcow2`.

The new name must name a file: one that is empty, ends in `/`, or whose last
part is `.` or `..` is refused. The folder must already exist, and none of
`Snapshot`, `Clone`, and `Rename` replaces an existing file.

`Snapshot`, `Commit`, `Rebase`, and `Resize` run through minimega, so the image,
the backing image `Rebase` sets, and any new name must be paths minimega can
take as one argument: no white space or control characters, and none of `"`,
`'`, `` ` ``, `\`, `#`, `*`, `?`, `[`, `]`, `{`, `}`, `$`, or `,`. A new name
from `Clone` or `Rename` follows the same rule. phēnix clones, renames, deletes,
and downloads the file itself, so those work on an existing image of any name.
A VM disk that an update or redeploy of a VM sets must follow the same rule.

!!! warning
    phēnix does not check whether other images are built on the one you rename
    or delete. Images backed by a renamed image must be rebased onto its new
    name, and images backed by a deleted one can no longer be used.

### Cloning in the background

`POST /api/v1/disks/clone/jobs?disk=&new=` makes the same checks as
`POST /api/v1/disks/clone`, then answers at once with a
[background job](jobs.md) that copies the image, counting the bytes copied, and
can be canceled. The copy is written to a hidden temporary file beside the new
image (`.<new name>.partial-<random>`), which the disk list leaves out, and
takes the new name only once it is complete. A canceled or failed clone deletes
it, and when the server restarts mid-clone, it deletes what the clone left the
next time it starts (in minimega's files directory once minimega answers), so an
interrupted clone never leaves an image that looks complete.

The server makes the same checks, whatever sends the request, and the error
names the reason. It answers 400 for an image outside the minimega files
directory or at a path the list leaves out, a folder rather than an image, a new
name that does not name a file, or a name minimega cannot use; 404 when the
image, or the folder for a new one, does not exist; and 409 when the new name is
taken.

## Image Details

`GET /api/v1/disks/details?disk=<full path>` reads one image of the list again,
and gives more than the list: the qcow2 or qcow header, the partition table and
file systems of the disk that the image holds, the listed images built on it,
and the VMs of each experiment that uses it. It needs the same permissions as
the list, and gives the VMs only for an experiment that your role may `get`.
phēnix does not write to the image.

phēnix reads the partitions of a raw image from the file. For a qcow2 or qcow
image, it reads them through the backing chain with `qemu-img`. It reads at most
16 MiB of the disk, for at most 30 seconds. When phēnix cannot read a part, the
response gives the reason in its place. For example, this occurs for an image
that only minimega can see, or for a container image.

## How phēnix Inspects Images

When `qemu-img` is installed — it is in the phēnix container image — phēnix
inspects images itself, several at a time, instead of running one minimega
`disk info` per image in turn. It reads each image's size, virtual size, format,
and backing chain that way, and can read an image a running VM holds locked.
Without `qemu-img`, phēnix falls back to asking minimega, and lists the folders
with minimega's `file list`, which, like phēnix's own search, does not follow
symlinked folders. The fallback leaves out the folders and images whose names
minimega cannot take, including images a topology names, and anything phēnix
sees as something other than a regular file.

An image is inspected again only when its file, or a file in its backing chain,
has changed since the last inspection. phēnix inspects every image at startup,
and again whenever the minimega files directory or a folder the list searches
changes and then stays quiet for a couple of seconds, so a long upload is not
inspected while it is still being written. When that happens, the server sends
a `disks` websocket message.

phēnix follows folders created later, and stops following removed ones. It
follows at most 4096 folders, and logs once when it stops at that limit, or at
the kernel's inotify limit, which the `fs.inotify.max_user_watches` sysctl
raises. Changes it does not follow, including to images outside the files
directory, show the next time the list loads.

`GET /api/v1/disks?refresh=true` has the server inspect every image again from
scratch, changed or not.

phēnix asks minimega for its files directory (its `-filepath`) the first time
it needs it. Until minimega answers, phēnix uses `<base-dir.phenix>/images`,
logs `minimega did not give its files directory`, and asks again at most once a
minute. When minimega answers with a different directory, phēnix lists and
watches that one instead.

## Broken Backing Chains

qemu cannot open an image when one of these is true:

* A file in the backing chain is missing.
* The chain names one of its own images again (a loop).
* The chain has more than 64 images.

`GET /api/v1/disks` gives the problem in three fields of each disk:
`brokenChain`, `chainProblem` (one sentence, for example
`backing file /phenix/images/ro_kali.qc2 is missing`), and `missingBacking`
(the full path of the missing file, or empty for a loop). For a broken chain,
`backingImages` lists only the images that phēnix could read. The CLI has no
command that lists these fields.

phēnix marks an image only when it can read each image in the chain, up to the
problem. It does not mark these images:

* An image backed by a vmdk or qed image, or by a backing format other than
  qcow2 or raw. phēnix does not examine the chain past that image.
* An image that phēnix cannot open, such as an image that only minimega can
  see.

The fields show the view of the head node. A file that is present on a compute
node but missing on the head node also shows as missing.

## Image Format Versions

phēnix reads the format and version in the header of each image. The list
gives them as `format` and `formatVersion`, and gives a `formatIssue` with a
level and a fix for an image that needs care:

| Image | `formatIssue` level |
| :--- | :--- |
| qcow2 version 3 (`compat=1.1`), raw | None. |
| qcow2 version 2 (`compat=0.10`) | `warning` |
| qcow version 1 | `warning`. Some qemu builds, such as the `qemu-kvm` of RHEL, cannot open this format. |
| qcow2 or qcow version 1 with the old AES encryption | `error`. qemu does not open these images for VMs. |
| qcow2 with LUKS encryption | `error`. phēnix cannot give qemu the key. |
| qcow2 of a version later than 3, or with feature bits that qemu does not know | `error`. A newer qemu made the image. |
| qcow2 that qemu marked as corrupt | `error` |
| qcow2 that qemu did not close cleanly | `warning` |
| vmdk or qed | `warning`. phēnix does not check past this image. |
| An image whose header phēnix cannot read | `warning` |

An image whose backing chain holds an image with an `error` level gets the
issue of that image, unless it has an `error` of its own.

The list also gives an image that `qemu-img` cannot open for its version or
feature bits. It finds a qcow version 1 image in the files directory only when
its name has a known extension, such as `.qcow`. It also gives an image with a
different name when a topology uses it, or when it backs a listed image.

Before you change an image, stop each VM that uses it. To fix an image, use
the command for its problem:

1. To upgrade a qcow2 version 2 image to version 3, change it in place:

    ```bash
    qemu-img amend -f qcow2 -o compat=1.1 kali.qc2
    ```

2. To convert a qcow version 1 image to qcow2, write a full copy. Then point
   each image that it backs at the copy:

    ```bash
    qemu-img convert -f qcow -O qcow2 kali.qcow kali.qc2
    qemu-img rebase -u -F qcow2 -b kali.qc2 overlay.qc2
    ```

3. To remove the old AES encryption, write a copy without encryption. Put
   only the passphrase in a file, with no newline at the end:

    ```bash
    printf '%s' 'kalipass' > passphrase.txt
    qemu-img convert --object secret,id=sec0,file=passphrase.txt \
      --image-opts driver=qcow2,file.filename=kali.qc2,encrypt.key-secret=sec0 \
      -O qcow2 kali-plain.qc2
    ```

    For a qcow version 1 image, use `driver=qcow`. The same command removes
    LUKS encryption. The old AES encryption does not check the passphrase. A
    wrong passphrase, or a newline after it, gives a copy that holds random
    data, so examine the copy before you use it.

4. To repair an image that qemu marked as corrupt or did not close cleanly,
   copy it first, then repair it:

    ```bash
    cp kali.qc2 kali-backup.qc2
    qemu-img check -r all kali.qc2
    ```

5. A newer qemu can make an image that an older qemu cannot open. To fix
   it, write a copy with the `qemu-img` of the newer qemu. The copy has no
   zstd compression, extended L2 entries, or external data file. Run this
   command:

    ```bash
    qemu-img convert -O qcow2 -o compat=1.1 kali.qc2 kali-compat.qc2
    ```

## Downloading an Image

The server serves only images it acts on: in the minimega files directory or a
folder under it, but not at a path the list leaves out. A path with `../` in
it, a sibling directory whose name merely starts with that directory's name, and
an image outside the directory are all refused.

Image downloads resume: if one is interrupted, retrying it in the browser's
downloads list continues where it stopped, as long as the image has not
changed. An image a running VM writes to changes during the download, which
then starts over from the beginning, so stop the VM first for a consistent
copy. From a shell, `curl -C -` resumes a download the same way, but without
checking that the image is unchanged:

```bash
curl -C - -o win.qc2 -H 'X-Phenix-Auth-Token: Bearer <token>' \
  'https://<phenix>/api/v1/disks/download?disk=win.qc2'
```

To verify a download, compare its `sha256sum` with the `sha256` that
`GET /api/v1/disks/checksum?disk=win.qc2` returns. phēnix computes it in the
background, which takes minutes for a large image; until then the route
answers `202`, so ask again later.
