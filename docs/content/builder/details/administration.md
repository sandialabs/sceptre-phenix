# Administration: Details

!!! info
    This page goes deeper than the short guide:
    [Administration](../administration.md).

## Availability

Every phēnix server has Builder, and no setting turns it on or off. The
editor is at `/builder`, and its API is under `/api/v1/builder` (see
[REST API](#api)).

phēnix sets no `Content-Security-Policy` on the application page. If a proxy
in front of phēnix adds one, it must allow workers from the phēnix origin
and `data:` images: the layout engine and `Auto-group` by name pattern run
in workers, and custom icons are `data:` images.

## Permissions

`builder` `use` also covers the Builder schemas that give the fields of the
Inspector. The verb `"*"` gives `use` too. Any Builder user can open, change
and delete any draft.

`builder` `use` gives no access to configs (see
[What Each Task Needs](#what-each-task-needs) and
[Resource: `configs`](../../user-administration.md#resource-configs)). A
role that limits topology names doesn't limit which Builder files its users
can read (see [Who Can Read a Builder File](#who-can-read-a-builder-file)).

With authentication disabled, everyone works as `global-admin` with the
Global Admin role, so drafts show `global-admin` as the user who created
and changed them. `Global Viewer` doesn't have `builder` `use`.

Without `builder` `use`, the `Configs` page has no Builder links or
buttons. A refused API request answers 403 with a message such as
"deleting a Builder draft: user bob does not have permission", and a draft
that doesn't exist answers 404.

### The Builder Role

[Builder (`builder`)](../../user-administration.md#builder-builder) shows
the role as YAML. It gives no access to User, Role or Image configs. Each
time `phenix ui` starts:

- If the store has no role named `builder` and no role with the role name
  `Builder`, phēnix creates it, so a deleted role comes back.
- If the role exists, phēnix keeps its policies. If it lacks `builder`
  `use`, phēnix adds the `builder` policy to the role and to its users.
  Nothing else changes.

### What Each Task Needs

`builder` `use` alone covers drafts and snapshots, undo and redo, the
libraries, the default layer set, the Inspector fields, and `Upload` of a
Builder document from a file or pasted text. Other tasks also need:

| Task | Other permissions |
|---|---|
| See the `Published Diagrams` tab | `configs` `list` on the topologies it lists |
| Open a published diagram (`Open`, `Edit as a draft` or `Upload`) | `configs` `get` on that topology, such as `Topology/riverside-water` (this doesn't protect a [Builder file](#who-can-read-a-builder-file)) |
| Import a stored config | `configs` `get` on the config, and `topologies` `list` (or `experiments` `list`) on its name |
| Import a config file, or convert a legacy Builder diagram with `Upload` | `configs` `get` and `create` |
| Upload a Builder package | `configs` `get`, and `create` to create the configs you tick |
| Open a topology in Builder from the `Configs` page | `configs` `list`, and `get` on the topology (`create` to import it) |
| See `Exp`, which opens the experiment of a publication | `experiments` `get` on that experiment |
| Add a stored scenario to a diagram | `configs` `list` and `scenarios` `list` |
| View, edit, create or upload a scenario in the `Scenarios` dialog | `configs` on `Scenario/<name>`: `get` to view, `update` to edit, `create` to create or upload, and `get` and `update` to replace a scenario with an uploaded file |
| See the apps of the diagram's scenario in the Inspector | `configs` `get` on `Scenario/<name>` |
| Download `Topology YAML` or a package | `configs` `get` |
| Use `Publish`, in the editor or on a draft card | `configs` `update` |
| Publish a topology | `configs` `create` (new) or `update` (existing) on `Topology/<name>`. For a draft imported from a stored config, also `configs` `get` and `topologies` `get` (or `experiments` `get`) on that config |
| Publish an experiment | `experiments` and `configs` `create` (new) or `update` (existing) on `Experiment/<name>` |
| Publish a diagram that lists scenarios | `configs` `get` and `scenarios` `list` on each listed `Scenario/<name>`, and `configs` `update` on each scenario whose `topology` annotation doesn't name the topology yet |
| See which host pins an experiment update removed | `experiments/schedule` `get` (otherwise the warning only counts them) |
| Delete a published topology | `configs` `get` and `delete` on `Topology/<name>` |
| Get drive image suggestions and missing-image checks | `disks` `list` |
| Run `Preflight` | `configs` `get`, plus per check (a check without its permission shows as unavailable): `Host capacity`, `hosts` `list`. `Networks`, `hosts` `list`, `experiments` `list` for the VLANs of running experiments, and `configs` and `experiments` `get` on the named experiment. `Disk images`, `disks` `list`. `Scenario apps`, `applications` `list`, and `configs` `get` and `scenarios` `list` on each listed scenario |

Import and Publish read included topologies, such as `corp-services`, with
the user's own `configs` `get` and `topologies` `list` permissions, and warn
about an include the user can't read. An import of a config file keeps each
`${NAME}` and `${NAME:default}` as written, so Builder never shows the
server's environment variables.

### The Libraries

No role can change these library items, Global Admin included (see
[The template library](templates.md#the-template-library),
[Layer sets](layers.md#layer-sets) and
[Custom icons](icons.md#custom-icons)):

| Item | A change answers |
|---|---|
| The five built-in templates, and the templates and collections of [template files on the server](#template-files-on-the-server) | 409, `template.item.read-only` |
| The built-in layer set `Purdue Model` (ID `builtin:purdue`) | 403, `layerset.read-only` (also for a delete) |
| The icons that phēnix adds from template files | 403 (rename or delete) |

Any Builder user can rename or delete an uploaded icon. When someone deletes
the default layer set (see
[Layers for new diagrams](layers.md#layers-for-new-diagrams)), the server
has no default. A diagram keeps its own copies of templates and layers, so
a library change doesn't change the diagram (see
[Linked templates](templates.md#linked-templates)).

phēnix logs each add, change or delete of a template, collection or layer
set, each choice of a default layer set, and each upload, rename or delete
of an icon, with the user name.

### Example Role

The example role
[topology-designer.role.yaml](../examples/roles/topology-designer.role.yaml)
has `builder` `use`, `configs` `list` and `get` on `Topology/*`,
`Scenario/*` and `Experiment/*`, `list` and `get` on topologies, scenarios
and experiments, and `disks` `list`. Its users can import stored configs,
open published diagrams and download Topology YAML. To let the role
publish, add `create`, `update` and `delete` to its `configs` policy.

## Storage

Drafts, published documents and the three libraries are records in the
phēnix store (the `store.endpoint` setting), in namespaces that start with
`builder.`. Their limits are in [Limits](../reference.md#limits). The
built-in templates and layer set are part of phēnix, not store records, and
phēnix keeps the collections of template files in memory only.

The libraries belong to the server. When someone deletes a user account,
its icons, templates and layer sets stay, and `createdBy` and `updatedBy`
keep the user name.

A publish stores a copy of the diagram that never changes, the published
document, which the
[builder-doc annotation](#the-builder-doc-annotation) names. After a
publish, phēnix deletes the topology's older published documents that are
more than one hour old. When anyone deletes or renames a topology, by any
means, phēnix deletes its published documents (one less than an hour old
can stay for a while, in case a publish is in progress). This never reads,
changes or deletes a Builder file.

The browser keeps unsaved changes in the IndexedDB database
`phenix-builder`, and preferences in localStorage keys that start with
`phenix.builder.`. Logging out deletes the unsaved changes (see
[Logging out](drafts.md#logging-out)).

### Cleaning Up Storage

When phēnix starts, and every hour while it runs, it deletes the published
documents that no topology references, and the stored document content that
no draft or published document names. It keeps:

- what phēnix stored in the last hour, since a publish or a save may still
  reference it
- every published document, when a topology can't be read or its
  `builder-doc` annotation doesn't decode
- a published document that it can't read, and its content
- every Builder file

phēnix leaves a published document it can't read (a newer phēnix may have
saved it) out of every list, and logs
`skipping unreadable builder published document` with the ID. To delete
it, send `DELETE /builder/documents/{document}` with that ID, which changes
no config. The role needs `configs` `get` and `delete` on the topology the
record names, or on `*/*` when it names none. For `If-Match`, send any tag:
the 412 answer gives the right one in its `ETag` header.

`Clean up…` in the Builder settings does the same at once, and also deletes
each draft whose current snapshot is the last one it published, with no
newer snapshot, while its topology still references what it published. The
topology and its published document stay. A draft also stays when its
topology was deleted or published again from another draft (it's then the
only copy of its diagram), or when it changes while the cleanup runs.

A cleanup waits for a publish that is in progress. With `?dryRun=true`,
`POST /builder/cleanup` deletes nothing and tells you what it would delete:

```json
{"dryRun": true, "drafts": ["4b0c…"], "documents": 1, "content": 2}
```

`drafts` lists draft IDs, and `documents` and `content` count published
documents and stored document content. If the cleanup can't read or
delete an item, the answer has a `Warning` header and the server log gives
the reason. The answer still lists the deleted drafts.

## Template Files on the Server

The guide covers
[where the directory is and how to change it](../administration.md#template-files-on-the-server).
Each file becomes a read-only
[server collection](templates.md#server-collections), in the format that
the `Node Templates` tab downloads (see
[Downloading and importing templates](templates.md#downloading-and-importing-templates)).

phēnix reads at most 50 files directly in the directory, in name order:
names that end in `.yaml`, `.yml` or `.json` in any case, and that don't
start with `.`. It doesn't read subdirectories. A file must be a regular
file of at most 8 MiB, as JSON or YAML, and phēnix only follows a symbolic
link that stays in the directory. When the directory doesn't exist or can't
be read, phēnix has no server collections and Builder still starts.

For each file it skips, phēnix logs `skipping builder template file` with
the `file` and the `reason`. It logs
`builder template directory cannot be read` when the path isn't a
directory, and `read builder template files` with the counts.

The custom icons of a file go into the icon library under their names, if
the library doesn't have them yet. The icon dialog shows them as from
"Server". They fill at most 1,000 of the 2,000 icons; phēnix skips the rest
and logs `builder icon library has no room for more template file icons`.
When the library already has an icon of that name with a different
picture, the library keeps its picture, and phēnix logs
`builder template file icon differs from the icon library's, which is kept`.

The IDs of collections and templates come from the file name and the
template name, so they stay the same across restarts. Renaming a file gives
its collection a new ID, and a diagram links its copy of a template by that
ID (see [Linked templates](templates.md#linked-templates)).

## The builder-doc Annotation

The annotation is a map with up to three keys:

```yaml
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: pump-station
  annotations:
    builder-doc:
      digest: sha256:1c92d3b0c95588fbb20926c1e58bb0452903c4e2cb0ffbf12fcdc19f72717732
      id: fd063e784604f43e5c39cc9959501cf117b4dc2be895e92f9bb98e23f8465c4b
      path: /phenix/topologies/pump-station/pump-station.builder.json
spec:
  ...
```

| Key | What it holds |
|---|---|
| `digest` | `sha256:` and 64 lowercase hex digits: the published document with that content for this topology, or with `path`, the document the file must hold |
| `id` | The ID of a published document in the store, valid only for the topology that received it |
| `path` | The absolute path of a Builder file on the phēnix server (see [Builder Documents in Files](#builder-documents-in-files)) |

The map needs at least one of these keys, and no others. `Publish` writes
`digest` and `id`, and keeps a `path`. `builder-doc` is the only annotation
that is a map (an object, in JSON); all other annotations are text.

### Which Diagram a Topology Shows

1. The published document that the annotation names: by `id`, or by
   `digest` when there's no `id`. It must belong to this topology, and
   match the `digest`, if any.
2. Otherwise, the Builder file at `path`. It must hold the document with
   the `digest`, if any.
3. Otherwise, none. `Published Diagrams` doesn't list the topology, and on
   the `Configs` page its `builder` tag and edit button open the `Import`
   dialog with "Topology pump-station has no Builder diagram to open.
   Import it to make one."

The stored document comes first because Publish wrote the stored topology
from it, while the file may have changed since.

### What phenix Checks

phēnix checks the annotation each time anyone creates or updates a
Topology config, by any means, and refuses a config whose `builder-doc`
isn't valid. With `phenix config create`, the log line before the error
gives the reason:

```console
$ phenix config create pump-station.topology.yaml
2026-10-01 21:52:35.595 ERR calling config hook: config validation failed: topology pump-station: builder: invalid request: builder-doc.path: must be an absolute path type=SYSTEM uuid=197b8a05-ccb3-4162-994a-c39d8ebc1dc5
Error: Unable to create configuration from pump-station.topology.yaml (search error logs for 197b8a05-ccb3-4162-994a-c39d8ebc1dc5)
```

| Reason | What is wrong |
|---|---|
| `value must be an object` | `builder-doc` is text, not a map |
| `property "file" is unsupported` | A key other than `digest`, `id` and `path` |
| `there must be at least 1 properties` | The map is empty |
| `builder-doc: digest is not a sha256 digest` | `digest` isn't `sha256:` and 64 lowercase hex digits |
| `builder-doc.path: must be an absolute path` | `path` doesn't start with `/` |
| `builder-doc.path: must be a clean path, with no ".", "..", "//" or trailing "/"` | `path` has one of these |
| `builder-doc.path: must end in .json, .yaml or .yml` | `path` has a different ending, or an ending in capitals, such as `.JSON` |

A `path` can be at most 1024 bytes. phēnix doesn't look for the file when
it stores the config.

`phenix config edit` can't delete an annotation. To delete `builder-doc`,
send the config without it with `PUT /api/v1/configs/topology/<name>`, or
delete the topology and create it again.

### Renaming and Copying a Topology

A published document belongs to one topology name. After a rename, or a
copy of the config under a new name, the `id` and `digest` no longer name a
document of that topology, so phēnix changes the annotation:

- Without a `path`, phēnix deletes the annotation. The topology is a plain
  Topology config until someone publishes it again.
- With a `path`, phēnix deletes the `id`. The `path` and `digest` stay, so
  phēnix reads the topology from the file, which must still hold the
  document with that digest.

## Builder Documents in Files

Instead of a published document, a topology can name a Builder document in
a file on the phēnix server, such as a repository you check out there. The
guide has the steps (see
[A Builder file beside a topology](../administration.md#a-builder-file-beside-a-topology)).

### Example

The directory `/phenix/topologies/pump-station/` holds two files:

- `pump-station.builder.json`, the Builder document (the example file
  [pump-station.builder.json](../examples/pump-station.builder.json)).
- `pump-station.topology.yaml`, the Topology config, with a `builder-doc`
  annotation that names the document:

    ```yaml
    apiVersion: phenix.sandia.gov/v1
    kind: Topology
    metadata:
        name: pump-station
        annotations:
            builder-doc:
                path: /phenix/topologies/pump-station/pump-station.builder.json
    spec:
        nodes:
            - general:
                description: Field engineering laptop
                hostname: eng-ws-01
            - ... # the rest of the nodes
    ```

Make the config from `Download` > `Topology YAML` (see
[Topology YAML](import-upload-download.md#topology-yaml)). Don't use the
example file [pump-station.topology.yaml](../examples/pump-station.topology.yaml)
here: its node order is different, and a draft made from the file can't
update a topology that differs from what the document publishes.

```console
$ phenix config create /phenix/topologies/pump-station/pump-station.topology.yaml
2026-10-01 21:52:35.788 INF configuration created type=SYSTEM kind=Topology name=pump-station
```

Its diagram opens read only (see
[Diagrams read from a file](drafts.md#diagrams-read-from-a-file)). phēnix
keeps no copy of the file, so after a `git pull` the next `Open` shows the
new diagram, but the stored Topology config doesn't change. To make it
match the file again, create the config again, or publish a draft made
from the file.

### Which Files phenix Reads

phēnix reads a Builder file only when all of these are true:

- It's below the phēnix base directory: `/phenix`, or the `base-dir.phenix`
  setting (see [Settings](../../settings.md#settings-reference)).
- It's not below the VM mount directory: `/phenix/mounts`, or the
  `mount-dir` setting.
- It's a regular file, not a directory, a device or a pipe. phēnix only
  follows a symbolic link whose target is a relative path that stays below
  the base directory and outside the mount directory, so it refuses an
  absolute target, such as from `ln -s /phenix/topologies/a.json b.json`.
- It's at most 5 MiB.
- It holds one valid Builder document, as JSON or YAML. The content sets the
  format, not the file name. A YAML file must not use anchors, aliases,
  merge keys or more than one document.

When the CLI creates the config from a file, phēnix fills in `${NAME}` in
the `path` from the environment. A config sent to the REST API only fills
in `${BRANCH_NAME}`. phēnix doesn't fill in `${NAME}` in the Builder file.

phēnix resolves the path on the server that runs `phenix ui`, so servers
that share one store each read their own file system.

phēnix only reads the file when someone opens the diagram, makes a draft
from it, or publishes that draft. `Published Diagrams` shows a card even
when the file is missing or invalid, and no config change or `phenix`
command reads the file.

### Who Can Read a Builder File

Anyone whose role has `configs` `get` on the topology can open its diagram,
and anyone who can list the config can see the `path`.

!!! warning
    The name of a topology doesn't protect its Builder file. A user who has
    `configs` `get`, and `create` or `update`, on any one topology name can
    store a topology there with any `path` below the base directory, and
    then open the diagram of that file.

    For example, a role limited to `Topology/teama-*` can still store
    `teama-x` with the `path` of a team B Builder file, and open it. A
    fixed layout such as `/phenix/topologies/<name>/` makes the path easy
    to guess.

    Only keep a Builder file below the base directory if each user who can
    create or update a topology may read it. A published document doesn't
    have this problem, because it belongs to its topology.

### Pinning the File with a Digest

Add `digest` beside `path` to accept only one document:

```yaml
builder-doc:
  digest: sha256:1c92d3b0c95588fbb20926c1e58bb0452903c4e2cb0ffbf12fcdc19f72717732
  path: /phenix/topologies/pump-station/pump-station.builder.json
```

If the file holds a different document, the diagram doesn't open.

The digest is the SHA-256 of the document as phēnix writes it, not of the
file, so `sha256sum` of a YAML file, or of JSON with different spacing, key
order or final line break, gives a different value. Get the digest from the
REST API (see [Examples](#examples)).

### When the File Cannot Be Used

Opening the diagram then fails with "Could not open the diagram of topology
pump-station." and one of these sentences, which the REST API gives as
`message`. The phēnix log says `builder document file not usable`, with the
topology, the path and the reason.

| Message | Status | Why |
|---|---|---|
| "Builder file … is outside /phenix, the directory phenix reads Builder files from." | 422 | The path isn't below the base directory, or it's below the mount directory |
| "Builder file … does not exist on this phenix server." | 404 | No file is at the path, or a part of the path is a file |
| "phenix cannot read Builder file …." | 422 | phēnix has no read permission, a symbolic link isn't allowed, or the read failed |
| "Builder file … is not a regular file." | 422 | The path is a directory, a device or a pipe |
| "Builder file … is larger than 5 MiB." | 413 | The file is too large |
| "Builder file … is not a valid Builder document. Upload it in the Builder to see why." | 422 | `Upload` shows the problem (see [Uploading a Builder document](import-upload-download.md#uploading-a-builder-document)) |
| "Builder file … does not match the digest topology pump-station records for it." | 422 | The file holds a document with a different `digest` |

### Editing and Publishing

A draft made with `Edit as a draft` from the file can update the topology
under the rules in
[Publishing to a topology that names a Builder file](publishing.md#publishing-to-a-topology-that-names-a-builder-file).
Publish never writes the file. It stores a published document and writes
`digest` and `id` beside the `path`:

```yaml
builder-doc:
  digest: sha256:4ba9ae46494bb7d2498f5119b55996f332c52c1b95d79502361afbf50707cbc3
  id: 89f0a9779c2e5892da9f7a9ae2ed5c58c564fe57ea86f50f4452f174cb68ad93
  path: /phenix/topologies/pump-station/pump-station.builder.json
```

The topology then shows the published document, without the `File` tag,
and the file is out of date until you replace it. The `path` stays for any
other phēnix server that gets this config without the published document:
that server reads its own file, if the file matches the `digest`.

## API

Scripts use the same REST API as the web UI, under `/api/v1`. The
interactive API docs at `/docs/` describe each route under the `Builder`
tag (see
[Interactive API Docs](../../api.md#interactive-api-docs-swaggeropenapi)).
No single request publishes a Builder file: create a draft from the
document, then publish the draft.

Each route needs `builder` `use`. These routes also need `configs`
permissions:

| Route | What it does | `configs` permission |
|---|---|---|
| `POST /builder/drafts/{draft}/publish` | Publish the draft. With `"dryRun": true`, report what publishing would change and write nothing (no `If-Match` needed) | That of each config |
| `POST /builder/drafts/{draft}/preflight` | Run the [preflight checks](checks.md#preflight) named in `checks` (`capacity`, `network`, `disks`, `apps`), against the named `experiment`, if any | `get`, plus those of each check (see [What Each Task Needs](#what-each-task-needs)) |
| `GET /builder/sources` | The configs that a document can come from or publish with | `list` |
| `POST /builder/generate` | Make a document from a stored config or a config file, with the [import options](import-upload-download.md#import-options) (`includes`, `copy`, `name`) | `get`, and `create` for a config file |
| `POST /builder/legacy` | Convert a [legacy Builder](../legacy.md) diagram, or a Topology config file that holds one | `get` and `create` |
| `POST /builder/export/topology` | The Topology YAML that a document would publish | `get` |
| `POST /builder/package` | The [Builder package](import-upload-download.md#moving-a-diagram-with-a-builder-package) of a document, with the sections in `include` | `get`, and for each config in the package, `get` with `scenarios` or `topologies` `list` |
| `POST /builder/package/resolve` | Whether each item the package needs is `present`, `missing`, `different` or `unknown` here | `get`, `list` for icons, `disks` `list` for disk images, `applications` `list` for apps |
| `GET /builder/documents` | The published documents (`source` `store`), and the topologies that name a Builder file (`source` `file`, with `path` and no `id`) | `list` |
| `GET /builder/documents/{document}` | Read a published document | `get` on its topology |
| `DELETE /builder/documents/{document}` | Delete a published topology and its published documents, or an unreadable published document | `get` and `delete` on the topology |
| `GET /builder/topologies/{topology}/document` | Read the diagram that `builder-doc` names, from the store or the file | `get` on the topology |

These routes need only `builder` `use`:

| Route | Notes |
|---|---|
| `GET /schemas/builder/v1`, `/schemas/builder/templates/v1`, `/schemas/builder/package/v1`, `/schemas/builder/layersets/v1` | JSON Schemas of the Builder document, template file, package and layer set file |
| `GET`, `POST` `/builder/drafts` | The list has `drafts` (latest change first), `damaged` and `limits.drafts` |
| `GET`, `DELETE` `/builder/drafts/{draft}` | `DELETE` also works on a damaged draft |
| `GET`, `POST` `/builder/drafts/{draft}/snapshots` | `POST` saves a new version |
| `GET`, `DELETE` `/builder/drafts/{draft}/snapshots/{snapshot}` | You can't delete the `current` snapshot |
| `PATCH /builder/drafts/{draft}/cursor` | Undo and redo: choose the current snapshot |
| `POST /builder/cleanup` | See [Cleaning Up Storage](#cleaning-up-storage) |
| `GET`, `POST` `/builder/icons` | The list has no images, but gives `maxIcons` and an `etag` for `If-None-Match` (304 while unchanged) |
| `GET`, `PUT`, `DELETE` `/builder/icons/{icon}` | `GET` also works with an old name. `PUT` renames an uploaded icon, keeping at most 16 old names |
| `GET /builder/templates` | The library (`templates`, `collections`), `builtins` and the server collections (`preloaded`) |
| `POST /builder/templates/items`, `PUT /builder/templates/items/{template}` | Add templates (with `collection`, in a new collection), or replace one |
| `POST /builder/templates/collections`, `PUT /builder/templates/collections/{collection}` | Add or replace a collection |
| `POST /builder/templates/delete` | Delete templates and collections |
| `GET`, `POST` `/builder/layersets` | Built-in sets first, then by name, with `defaultId`, `defaultEtag`, `damaged` and the limits |
| `GET`, `PUT` `/builder/layersets/default` | `"id": null` clears the default |
| `GET`, `PUT`, `DELETE` `/builder/layersets/{layerset}` | Not for a built-in set (`PUT`, `DELETE`) |

These requests need an `If-Match` header with the item's `etag` from the
last response: each change to an existing draft, `PUT` and `DELETE` of a
layer set, `PUT` of a template or collection,
`PUT /builder/layersets/default`, and `DELETE` of an icon or a published
topology. Without it, the request answers 400. An older tag answers 412
(`template.item.stale`, `layerset.stale` or `request.stale`) with the
current ETag, so read the item again, check it, and resend.
`POST /builder/templates/delete` takes each item's `etag` in the body
(`{"templates": [{"id": "…", "etag": "\"3\""}]}`), and answers 412 and
deletes nothing if any of them changed.

Use the `etag` in the body of draft responses rather than the `ETag`
header, which a compressing proxy can change. Templates, collections, layer
sets and icons carry `createdBy` and `updatedBy`; an item that no user
added, such as a built-in template, has no `createdBy`.

With authentication enabled, send a token in the `X-Phenix-Auth-Token`
header as `Bearer <token>` (see
[Generating User Authentication Tokens](../../user-administration.md#generating-user-authentication-tokens)).
A refused request answers with a JSON body whose `code` names the failure,
such as `publish.topology.exists` (see
[Errors of the REST API](../reference.md#errors-of-the-rest-api)).

### Response Headers

Every Builder response, the schema routes included, has
`X-Content-Type-Options: nosniff` and `Cache-Control: no-store`, so no
browser or proxy keeps a copy. The icon responses also have
`Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`.

### Examples

These examples use the [example lab](../tutorial.md#the-example-lab) and
`jq`. Set the server and your token first:

```bash
PHENIX=http://localhost:3000
TOKEN='your-token'   # a token from the Users page
```

List the drafts:

```bash
curl -s -H "X-Phenix-Auth-Token: Bearer $TOKEN" "$PHENIX/api/v1/builder/drafts" \
  | jq '.drafts[] | {title, createdBy, id, etag}'
```

```json
{
  "title": "Riverside Water",
  "createdBy": "darrow",
  "id": "6290aef3-24a3-4126-b90a-308cbf561f17",
  "etag": "\"10\""
}
```

Make a document from the stored topology `riverside-water`, as `Import`
does. `POST /builder/generate` writes nothing:

```bash
curl -s -X POST -H "X-Phenix-Auth-Token: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"source":"Topology/riverside-water"}' "$PHENIX/api/v1/builder/generate" > generated.json
jq '{name: .document.metadata.name, source: .source.fullName, warnings}' generated.json
```

```json
{
  "name": "riverside-water",
  "source": "Topology/riverside-water",
  "warnings": [
    "The Builder added 2 nodes from included topology corp-services (2 nodes). The diagram shows them read only. Edit them in their own topology. Publishing keeps includeTopologies and does not copy them."
  ]
}
```

Store the document as a draft. The `sourceToken` connects the draft to its
topology, so the draft can publish back to `riverside-water`:

```bash
jq '{title: .document.metadata.name, sourceToken: .source.fullName, document}' generated.json \
  | curl -s -X POST -H "X-Phenix-Auth-Token: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d @- "$PHENIX/api/v1/builder/drafts" > created.json
jq '{id, createdBy, title, etag}' created.json
```

To keep the name of an uploaded file (the Inspector's `Source file`), send
`sourceFile` beside `document`: a plain file name of at most 255 bytes,
with no `/` or `\`. The answers to creating and saving a draft have no
document; they give `createdBy`, `createdAt`, `updatedBy` and `updatedAt`
as `stamp` (see
[Who made and last saved a diagram](import-upload-download.md#who-made-and-last-saved-a-diagram)).

Convert a [legacy Builder](../legacy.md) diagram, as `Upload` does.
`content` is the XML of the diagram, or a Topology config that holds it in
`builder-xml`, and `name` names a diagram that has no topology. The answer
has the same form as for `POST /builder/generate`:

```bash
jq -Rs '{content: ., name: "plant"}' plant.xml \
  | curl -s -X POST -H "X-Phenix-Auth-Token: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d @- "$PHENIX/api/v1/builder/legacy" > converted.json
jq '{name: .document.metadata.name, warnings}' converted.json
```

Delete the draft with its current ETag. The delete answers 204, or 400
without `If-Match`:

```bash
ID=$(jq -r .id created.json)
ETAG=$(curl -s -H "X-Phenix-Auth-Token: Bearer $TOKEN" "$PHENIX/api/v1/builder/drafts/$ID" | jq -r .etag)
curl -s -X DELETE -H "X-Phenix-Auth-Token: Bearer $TOKEN" -H "If-Match: $ETAG" \
  "$PHENIX/api/v1/builder/drafts/$ID"
```

Read the diagram of the topology `pump-station` (see
[Builder Documents in Files](#builder-documents-in-files)), without the
document itself:

```bash
curl -s -H "X-Phenix-Auth-Token: Bearer $TOKEN" \
  "$PHENIX/api/v1/builder/topologies/pump-station/document" | jq 'del(.document)'
```

```json
{
  "source": "file",
  "digest": "sha256:1c92d3b0c95588fbb20926c1e58bb0452903c4e2cb0ffbf12fcdc19f72717732",
  "size": 6517,
  "target": "pump-station",
  "kind": "Topology",
  "config": "Topology/pump-station",
  "path": "/phenix/topologies/pump-station/pump-station.builder.json"
}
```

`digest` is the value to write beside `path` (see
[Pinning the File with a Digest](#pinning-the-file-with-a-digest)). For a
file, the answer has `"topologyDiffers": true` when the stored topology
isn't what the document publishes. For a published document, `source` is
`store`, and the answer has `id`, `createdBy` and `createdAt` instead of
`path`.

## Compressed Files

The UI build writes a Brotli and a gzip copy of the large Builder scripts,
and phēnix sends the one the browser accepts, with a one-year cache (a
changed file gets a new name). `PHENIX_BROTLI_QUALITY` sets the Brotli
quality of the UI build: a whole number from 0 to 11, by default 9. Higher
is smaller and slower to build, and any other value stops the build. The
Docker and Podman images (`docker/Dockerfile`, which `make docker` and
`make deb` use unless `PHENIX_BUILD_IMAGE` names another image) build with
quality 11.

```bash
cd src/js
PHENIX_BROTLI_QUALITY=11 npm run build

# or, for the Docker image
docker build --build-arg PHENIX_BROTLI_QUALITY=9 -t phenix -f docker/Dockerfile .
```

## Troubleshooting

The problems an administrator can fix, such as a missing permission, a
Builder file that phēnix can't use, or a proxy that blocks the layout
engine, are in [Troubleshooting](../troubleshooting.md), with the steps for
each.

## See Also

- [User Authn/Authz in phēnix](../../user-administration.md#resources):
  every resource and verb a role can hold.
- [Settings & Configuration](../../settings.md): the `base-dir`,
  `store.endpoint` and `mount-dir` settings.
- [API](../../api.md#revisions-and-conditional-requests): ETags and
  conditional requests across the phēnix API.
