# Administration

This page is for phenix administrators. It tells which permissions Builder
users need, what the built-in Builder role holds, and where Builder keeps its
data. It also describes the `builder-doc` annotation, Builder documents in
files, the REST API, compressed files and troubleshooting.

## Availability

The Builder is part of every phenix server. You do not have to turn it on,
and no setting turns it off. Roles control who can use it (see
[Permissions](#permissions)):

- The navigation bar shows **Builder** to users whose role has `configs`
  `list`.
- The editor is at `/builder`.
- The Builder API routes are under `/api/v1/builder` (see
  [REST API](#rest-api)).

Builder works over plain HTTP. Only **Copy link** in the Share dialog needs
HTTPS, or a phenix opened as `localhost`. Otherwise the dialog shows the
link for the user to copy (see [Sharing a draft](drafts.md#sharing-a-draft)).
For HTTPS, see `--tls-key` and `--tls-cert` in `phenix ui --help`.

phenix sets no Content-Security-Policy on the application page, the page
that holds the Builder and the rest of the web UI. A proxy in front of
phenix can add one. That policy must let the Builder start workers from
the address of phenix, because the layout engine and **Auto-group** by name
pattern run in workers. It must also allow `data:` images, because Builder
draws custom icons from them.

## Permissions

Builder gives a user no more than the config permissions of their role, with
one exception: a role that is limited to some topology names does not limit
which Builder files its users can read (see
[Who can read a Builder file](#who-can-read-a-builder-file)).
Each Builder request needs the `configs` permission of the same verb:
`list` to list drafts, `get` to open one, `create` to make one, `update` to
save a change, and `delete` to delete one. See
[Resource: `configs`](../user-administration.md#resource-configs) and
[Roles](../user-administration.md#roles).

The authentication mode of the server changes how its users see Builder (see
[User Authn/Authz in phenix](../user-administration.md)):

- With authentication disabled, all users work as one user, `global-admin`,
  with the Global Admin role. They all see and change the same drafts, under
  **My Drafts**. No one can share a draft, because sharing needs user
  accounts.
- With authentication enabled, each user has their own drafts and can share
  them.

Three built-in roles have `configs` permissions:

- **Global Admin** can do everything. This includes opening, changing and
  deleting the drafts of all users, and publishing templates server-wide.
- **Builder** holds every Builder permission (see
  [The Builder role](#the-builder-role)).
- **Global Viewer** can open every draft and published diagram, read only.
  The drafts of other users appear under **Other users' drafts** with **Can
  view**.

The Experiment and VM roles have no `configs` permissions, so their users do
not see Builder. Give Builder users the Builder role, or a role such as the
[example roles](#example-roles) below.

### The Builder role

phenix has a built-in role for the people who draw and publish topologies:
**Builder** (`builder`). It holds every Builder permission:

- `configs` `list`, `get`, `create`, `update` and `delete`, on Topology,
  Scenario and Experiment configs only (`Topology/*`, `Scenario/*` and
  `Experiment/*`). It gives no access to User, Role or Image configs, so a
  Builder user cannot read accounts or change roles.
- `builder-drafts` `list`, `get`, `update` and `delete`, on every draft.
  With it, a user lists, opens, changes and deletes the drafts of other
  users (see [Other users' drafts](#other-users-drafts)).
- `builder-templates` `publish`. With it, a user publishes templates
  server-wide, and takes back the server-wide templates of any user (see
  [Server-wide templates](#server-wide-templates)).
- `builder-icons` `update` and `delete`. With it, a user renames and deletes
  the icons that other users uploaded (see
  [Icons of other users](#icons-of-other-users)).
- `schemas` `get`, for the fields of the Inspector.
- `topologies` and `scenarios` `list` and `get`, `experiments` `list`,
  `get`, `create` and `update`, and `disks` `list`, for Import, Publish and
  the drive image suggestions.

[Builder (`builder`)](../user-administration.md#builder-builder) shows the
role as YAML.

Each time `phenix ui` starts, it makes sure that the role exists:

- When the store has no role named `builder`, and no role with the role name
  `Builder`, phenix creates the role. A store made before the role existed
  gets it too, and a deleted role comes back at the next start.
- A stored role of that name, such as one that an administrator made, keeps
  its policies. If it cannot publish templates server-wide, or rename or
  delete the icons of other users, phenix adds the `builder-templates`
  `publish` or `builder-icons` `update` and `delete` policy that it does not
  have. The users that have the role get that policy too. Nothing else in the
  role changes.

Assign the role to a user on the **Users** page (see
[Updating Users](../user-administration.md#updating-users)). For a site
that wants to give its users less, such as no access to the drafts of other
users or no server-wide templates, use the [example roles](#example-roles)
instead.

### What each task needs

| Task | Permissions |
|---|---|
| See the **Builder** tab, and list drafts and published diagrams | `configs` `list` |
| Open a draft or a published diagram | `configs` `get`. For a published diagram, or the diagram of a topology that names a Builder file, the permission is on that topology, such as `Topology/riverside-water`. The name of the topology does not protect the file (see [Who can read a Builder file](#who-can-read-a-builder-file)) |
| Make a draft: **Blank diagram**, **Import**, **Upload**, **Edit as a draft**, **Save my history as a new draft** | `configs` `create` |
| Import a stored config | Also `configs` `get` on the config, such as `Topology/riverside-water`, and `topologies` `list` (or `experiments` `list`) on its name |
| Import a config file | `configs` `create` |
| Convert a legacy Builder diagram with **Upload** | `configs` `get` and `configs` `create` |
| Open a topology in Builder from the **Configs** page | `configs` `list`, and `configs` `get` on the topology. To import it, also `configs` `create` |
| Save changes, undo, redo, and restore or delete a snapshot | `configs` `update` |
| Delete your own draft | `configs` `delete` |
| Share your own draft | `configs` `update`, and authentication enabled |
| Publish from the card of a draft on the drafts page | The same as **Publish** in the editor |
| See **Exp**, which opens the experiment that a publication made | `experiments` `get` on that experiment |
| Add a stored scenario to a diagram | `configs` `list` and `scenarios` `list` |
| Store a scenario file from the **Scenarios** dialog | `configs` `create` on `Scenario/<name>`. To replace a scenario of that name, `configs` `get` and `configs` `update` on it |
| See the apps of the scenario of a diagram in the Inspector | `configs` `get` on `Scenario/<name>` |
| Download **Topology YAML** | `configs` `get` |
| Use the **Publish** button | `configs` `update` |
| Publish a topology | `configs` `create` on `Topology/<name>` for a new topology, `configs` `update` to update one. For a draft imported from a stored config, also `configs` `get` and `topologies` `get` (or `experiments` `get`) on that config |
| Publish an experiment | `experiments` `create` and `configs` `create` on `Experiment/<name>`. To update one, `update` of both |
| Publish a diagram that lists scenarios | `configs` `get` and `scenarios` `list` on each `Scenario/<name>` that it lists, and `configs` `update` on each scenario whose `topology` annotation does not name the topology yet |
| Delete a published topology | `configs` `delete` on `Topology/<name>` |
| Get the fields of the Inspector from the server | `schemas` `get` on `builder` |
| Get drive image suggestions and missing-image checks | `disks` `list` |
| Run **Preflight** checks on a draft | The same as to open the draft, and the permissions of each check. **Host capacity** needs `hosts` `list`. **Networks** needs `hosts` `list` for the bridges, `experiments` `list` for the VLANs that running experiments use (it compares only the experiments that it can list by name), and `configs` `get` and `experiments` `get` on the experiment named. **Disk images** needs `disks` `list`. **Scenario apps** needs `applications` `list`, and `configs` `get` and `scenarios` `list` on each `Scenario/<name>` that the diagram lists. Preflight reports a check, or a part of one, as unavailable when its permission is missing |
| List, open, change or delete the drafts of other users | `builder-drafts` (see [Other users' drafts](#other-users-drafts)) |
| See and use the icons of the server | `configs` `list` |
| Upload an icon, and add the icons of a diagram to the server with **Upload** | `configs` `create` |
| Rename an icon you uploaded | `configs` `update` |
| Delete an icon you uploaded | `configs` `delete` |
| Rename or delete the icon of another user | Also `builder-icons` `update` or `delete` (see [Icons of other users](#icons-of-other-users)) |
| See the **Node Templates** tab, use templates, and **Export** them | `configs` `list` |
| Add templates and collections, **Save to library**, **Copy to my library**, **Import templates**, **Restore built-in templates** | `configs` `create` |
| Edit templates and collections, add to and remove from a collection | `configs` `update` |
| Delete templates and collections | `configs` `delete` |
| Share templates and collections with users | `configs` `update`, and authentication enabled |
| Publish templates and collections server-wide | `configs` `update` and `builder-templates` `publish` (see [Server-wide templates](#server-wide-templates)) |
| Take back the server-wide template of another user | `configs` `update` and `builder-templates` `publish` |

Import and Publish read included topologies, such as `corp-services`, with
the `configs` `get` and `topologies` `list` permissions of the user. They
report an include that the user cannot read as a warning.

When a user imports a config file, phenix fills in `${NAME}` and
`${NAME:default}` from the environment of the server. Anyone whose role can
create configs can read the environment variables of the server (see
[Importing a config file](import-upload-download.md#importing-a-config-file)).

A share does not replace these permissions (see
[Sharing a draft](drafts.md#sharing-a-draft)).

### Other users' drafts

A user always sees their own drafts and the drafts shared with them. The
`builder-drafts` resource gives a role access to the drafts of other users
too, for example to help a user, or to delete the drafts of someone who
left:

| Verb | What it allows |
|---|---|
| `list` | List the drafts of other users under **Other users' drafts** |
| `get` | Open the draft of another user, read only |
| `update` | Change the draft of another user (shown as **Can edit**) |
| `delete` | Delete the draft of another user: **Delete** on its card, under **Other users' drafts**, or `DELETE /api/v1/builder/drafts/{owner}/{draft}` |

`builder-drafts` has no `create` verb. No verb lets a role share the draft
of another user: only the owner shares a draft. The role also needs the
`configs` permission of the same verb.

The resource names are `<owner>/<draft id>`. Use `"*/*"` for every draft, or
`alice/*` for the drafts of one user. A bare `"*"` matches no draft, because
`*` does not match the `/` in the name.

This policy lets a role list, open, change and delete the drafts of all
users:

```yaml
- resources:
  - builder-drafts
  resourceNames:
  - "*/*"
  verbs:
  - list
  - get
  - update
  - delete
```

This policy lets a role list and open the drafts of `alice` only:

```yaml
- resources:
  - builder-drafts
  resourceNames:
  - "alice/*"
  verbs:
  - list
  - get
```

![The drafts page of e2e-admin with the tab Other users' drafts, showing alice's draft Pump station with Can edit.](../images/builder/drafts-other-users.png)

### Server-wide templates

Users can publish templates and collections server-wide, for all users of
the server (see [Publishing server-wide](templates.md#publishing-server-wide)).
This needs `configs` `update` and the `builder-templates` resource with the
verb `publish`. The resource takes no resource names:

```yaml
- resources:
  - builder-templates
  verbs:
  - publish
```

A role with it can also take back the server-wide template or collection of
any user. The owner of an item can take it back without the permission. Of
the built-in roles, Global Admin and Builder have it.

The template library of a user is their own. No role, not even Global Admin
or a role with `builder-drafts`, reads or changes the library of another
user. The one exception is to take back a server-wide item. The security
log records each change of who an item is shared with, each server-wide
publication and its removal, and each request for the library of another
user.

### Icons of other users

The server keeps one icon library. Every user with `configs` `list` sees
and uses it (see [Custom icons](diagrams.md#custom-icons)). The user who
uploaded an icon can rename it, with `configs` `update`, and delete it, with
`configs` `delete`. The `builder-icons` resource lets a role rename and
delete the icons of all users, for example to fix names, or to remove
an icon that should not be there:

| Verb | What it allows |
|---|---|
| `update` | Rename the icon of another user. The old name still names the icon |
| `delete` | Delete the icon of another user, with all its names |

The resource takes no resource names. The role also needs the `configs`
permission of the same verb:

```yaml
- resources:
  - builder-icons
  verbs:
  - update
  - delete
```

Of the built-in roles, Global Admin and Builder have it. Without it, the
Custom icons dialog shows no **Rename** or **Delete** on the icon of another
user, and the server answers 403. The server logs each upload, rename and
delete of an icon, with the user who did it.

### Example roles

The [Builder role](#the-builder-role) gives its users everything. For a site
that wants to give less, two example roles cover the usual needs. Download
them from
[topology-designer.role.yaml](examples/roles/topology-designer.role.yaml)
and [topology-reviewer.role.yaml](examples/roles/topology-reviewer.role.yaml).

**Topology Designer** creates drafts, imports and uploads, and publishes
topologies, scenarios and experiments. It sees only its own drafts and the
drafts shared with it. It keeps its own template library and shares
templates with users, but cannot publish them server-wide:

```yaml
apiVersion: phenix.sandia.gov/v1
kind: Role
metadata:
  name: topology-designer
spec:
  roleName: Topology Designer
  policies:
  - resources:
    - configs
    resourceNames:
    - "Topology/*"
    - "Scenario/*"
    - "Experiment/*"
    verbs:
    - list
    - get
    - create
    - update
    - delete
  - resources:
    - topologies
    - scenarios
    resourceNames:
    - "*"
    verbs:
    - list
    - get
  - resources:
    - experiments
    resourceNames:
    - "*"
    verbs:
    - list
    - get
    - create
    - update
  - resources:
    - disks
    resourceNames:
    - "*"
    verbs:
    - list
  - resources:
    - schemas
    resourceNames:
    - "*"
    verbs:
    - get
```

**Topology Reviewer** opens the drafts shared with it and published
diagrams. It cannot create drafts, import, upload or publish. It lists the
templates shared with it and the server-wide templates, and can view them.
It cannot copy, share or publish them:

```yaml
apiVersion: phenix.sandia.gov/v1
kind: Role
metadata:
  name: topology-reviewer
spec:
  roleName: Topology Reviewer
  policies:
  - resources:
    - configs
    resourceNames:
    - "Topology/*"
    - "Scenario/*"
    - "Experiment/*"
    verbs:
    - list
    - get
  - resources:
    - topologies
    - scenarios
    - experiments
    resourceNames:
    - "*"
    verbs:
    - list
    - get
  - resources:
    - schemas
    resourceNames:
    - "*"
    verbs:
    - get
```

For `configs`, both roles name the three config kinds that the Builder
uses: `Topology/*`, `Scenario/*` and `Experiment/*`. Do not use `"*/*"`
there. It also matches User and Role configs, so a user could read password
hashes and change accounts and roles. To let Topology Designer manage the
drafts of all users, add the `builder-drafts` policy from
[Other users' drafts](#other-users-drafts). To let it publish templates
server-wide, add the `builder-templates` policy from
[Server-wide templates](#server-wide-templates).

To add the roles, store them as configs:

```bash
phenix config create topology-designer.role.yaml topology-reviewer.role.yaml
```

Then assign a role to a user on the **Users** page (see
[Updating Users](../user-administration.md#updating-users)).

!!! note
    phenix copies the policies of a role into a user when you assign the
    role. After you change a Role config, assign the role to its users again.

### What users without a permission see

Builder hides, or makes unavailable, what the role of a user does not allow.
The server still checks every request.

- Without `configs` `list`, the navigation bar has no **Builder** tab.
- Without `configs` `create`, the drafts page has no **Blank diagram**,
  **Import** and **Upload** buttons. It shows the note "Your role can open
  drafts and published diagrams, but not create drafts." An empty **My
  Drafts** tab says "You have no drafts." In the editor, **Upload** is
  unavailable. The command palette still lists **Blank diagram**, marked
  "Unavailable: Your role cannot create drafts."
- Without `configs` `update`, **Publish** is unavailable, and the command
  palette gives the reason "Your role cannot publish diagrams." The user
  cannot share their own drafts: their drafts show no **Share**. On a draft
  shared with the user, the toolbar shows **Share**, unavailable.
- Without `schemas` `get`, the Inspector shows a message that starts "Could
  not load this server's form fields." and ends "The Inspector shows the
  fields built into the Builder instead, which may differ from what this
  server accepts." The Inspector still works, with those built-in fields.
- Without `disks` `list`, the Inspector does not suggest drive images, and
  the **Diagram checks** dialog says "Drive images are not checked: the
  server did not list its disk images." The dialog says the same when the
  server lists no images, for example while minimega is not running.
- On the **Node Templates** tab, **New template** and **New collection**
  need `configs` `create`, **Edit** needs `configs` `update`, and
  **Delete** needs `configs` `delete`. A role without them sees the
  templates but not those buttons.
- In the **Custom icons** dialog, **Rename** and **Delete** show on the
  icons that the user uploaded, with `configs` `update` and `delete`. With
  `builder-icons` too, they show on every icon.
- Without `experiments` `get` on the experiment that a publication made,
  there is no **Exp** button for it.

For example, the role of bob, Topology Reviewer, cannot create drafts. His
drafts page looks like this:

![The drafts page of bob, whose Topology Reviewer role cannot create drafts: there are no Blank diagram, Import or Upload buttons, a note says Your role can open drafts and published diagrams, but not create drafts, and the empty My Drafts tab says You have no drafts.](../images/builder/drafts-view-only.png)

The server does not tell a user whether the draft of another user exists. A
draft that the user cannot see answers 404, as a missing draft does. A
request that the role or share of the user does not allow answers 403, with
a message such as "deleting a builder draft not allowed for bob".

## Storage

Drafts, published documents, the icon library and the template libraries
are records in the phenix store (the `store.endpoint` setting, bolt or
etcd), apart from configs. `phenix config list` and the Configs page do not
list them.

Limits:

- A draft keeps at most 50 snapshots and 50 MiB of them. Past either limit,
  phenix deletes the oldest snapshots.
- A diagram (a Builder document) can be at most 5 MiB. It can hold at most
  50 custom icons, 50 device templates and 100 notes of its own.
- The server holds at most 2,000 icons. An icon is a PNG of at most 96 × 96
  pixels and 40,960 bytes. For the limits of each user, see
  [Custom icons](diagrams.md#custom-icons).
- A template library can be at most 512 KiB. For all template limits, see
  [Limits](templates.md#limits).

An icon stays when the account of the user who uploaded it is deleted. It
still counts toward the 64 icons of that user name.

A template library belongs to a user name, as a draft does. Deleting a user
account removes nothing, and a new account with the same name gets the
library. With authentication disabled, the one library belongs to
`global-admin`. A user who never changed their template library has the
five built-in templates. The first change stores them as ordinary
templates, which the user can edit, delete and restore.

phenix keeps the server collections from its template files in memory, not
in the store. It reads the directory again at its next start (see
[Template files on the server](#template-files-on-the-server)).

The libraries use these store namespaces:

- `builder.icons`: one record for each icon, with the key `name/` and the
  icon name in lower case, and one record for each other name of an icon.
  At start, phenix deletes the icon records that an earlier version kept
  for each user, under the SHA-256 of the user name. It logs how many it
  removed. Diagrams that named these icons by their old keys show their
  built-in icons.
- `builder.templates`: one record for each user, with the key `lib/` and
  the SHA-256 of the user name. Sharing and server-wide publishing also
  write small records under `in/` and `pub/`. phenix never removes these
  records.

Publishing stores a copy of the diagram that never changes, the published
document, and names it in the `builder-doc` annotation of the topology (see
[The builder-doc annotation](#the-builder-doc-annotation)). The **Published
Diagrams** tab lists the topologies that name one, and the topologies that
name a Builder file instead (see
[Builder documents in files](#builder-documents-in-files)). After a publish,
phenix removes the older published documents of the topology when they are
more than an hour old.

When a topology is deleted or renamed, by any means, phenix removes its
published documents. A document less than an hour old can stay for a time,
because a publish of the topology may still be in progress. A renamed
topology is not listed under **Published Diagrams** until it is published
again, unless its annotation names a Builder file. Then the `path` stays,
and phenix reads the topology from that file (see
[Renaming and copying a topology](#renaming-and-copying-a-topology)).
Deleting or renaming a topology never reads, changes or removes a Builder
file.

The browser also keeps some Builder data:

- Changes not yet saved to the server stay in the IndexedDB database
  `phenix-builder` until the server stores them.
- Preferences stay in localStorage: `phenix.builder.theme`,
  `phenix.builder.panes`, `phenix.builder.minimap`,
  `phenix.builder.shortcuts` and `phenix.builder.settings`.
- Logging out deletes the rest: the unsaved changes, the recent commands,
  the last **Auto-group** name pattern and the **Preflight** checks last
  selected (`phenix.builder.preflight`). Signing in as another user in the
  same browser deletes them too (see [Logging out](drafts.md#logging-out)).

### etcd

Every Builder edit writes to the store. etcd keeps every earlier version of
every key until it compacts its history, and a Builder user makes many
edits. For this reason, when phenix uses etcd, phenix compacts the etcd
history itself. By default, it keeps one hour of history. The
`compaction-retention` parameter of the store endpoint changes this, as a
Go duration. `0` disables compaction, for clusters that you compact
yourself:

```yaml
store:
  endpoint: etcd://localhost:2379?compaction-retention=30m
```

!!! warning
    Compaction applies to the whole etcd cluster, not only to the keys of
    phenix. If other programs share the cluster and read older revisions,
    set a retention that is long enough for them, or set `0`.

When etcd reaches its space quota, it refuses every write, config writes
included. phenix then answers 507 with the message "etcd is out of space:
phenix cannot save changes until an administrator frees space (compact and
defragment etcd, then clear its NOSPACE alarm)". The editor keeps the
changes of the user in the browser and tries again. Its save state says
"Could not save your changes.", gives the message, and says "Saving retries
automatically." Publish reports the stages that failed. To recover, compact
and defragment etcd, then clear its `NOSPACE` alarm (see
[etcd maintenance](https://etcd.io/docs/latest/op-guide/maintenance/)).

## Template files on the server

To give all users of a server the same node templates, put template files in
the template directory of the server. When `phenix ui` starts, it reads each
file there as one collection of templates. Each user with `configs` `list`
sees the collection under its name, read only (see
[Server collections](templates.md#server-collections)). A file has the
format that the Node Templates tab exports, so you can put an exported file
in the directory without changes (see
[Exporting and importing templates](templates.md#exporting-and-importing-templates)
for the format, and the example
[node-templates.yaml](examples/node-templates.yaml)).

The directory is `<base-dir.phenix>/builder/templates` (by default
`/phenix/builder/templates`). The setting `base-dir.builder-templates`
changes it. Set it with the root flag `--base-dir.builder-templates`, the
environment variable `PHENIX_BASE_DIR_BUILDER_TEMPLATES`, or the key in the
phenix config file (see [Settings](../settings.md)). For example, in the
config file:

```yaml
base-dir:
  builder-templates: /etc/phenix/builder-templates
```

phenix reads:

- The files directly in the directory whose names end in `.yaml`, `.yml`
  or `.json` (in any case), at most 50, in the order of their names. It
  does not read subdirectories, or files whose names start with `.`.
- Each file as JSON or YAML, by its content, at most 8 MiB. It must be a
  regular file. phenix follows a symbolic link only when the link stays in
  the directory, as for [Builder files](#which-files-phenix-reads).
- Each file whole. phenix skips a file that is not valid, and reads the
  others.

phenix logs a skipped file as a warning, `skipping builder template file`,
with the `directory`, the `file` and the `reason`, for example `is not a
valid template file: templates[1].name: template name "plc" is also the
name of templates[0], ignoring case`. A directory that does not exist is not
an error: phenix logs it at debug level only, and has no server collections.
When phenix cannot open the path as a directory, for example because it is a
regular file, it logs the warning `builder template directory cannot be
read`, and has no server collections. In both cases, the Builder starts. At
the end, phenix logs `read builder template files` with the number of
collections that it read and the number of files that it skipped.

phenix adds the custom icons that a file carries to the icon library of the
server, under their names, when the library does not have them. These icons
have no owner, so no account owns them, whatever its name. The icon dialog
lists them as from "Server", and only users with the `builder-icons`
permissions can rename or delete them. They do not count toward the limits
of any user. They fill the library to at most 1000 of its 2000 icons, and
leave the rest for users. phenix skips the icons past that limit, and logs
one warning, `builder icon library has no room for more template file
icons`, with the number that it skipped. When the library has an icon of
the same name with a different picture, the library keeps its picture. phenix
then logs `builder template file icon differs from the icon library's, which
is kept` with the `file`, the `icon` and the `owner` of the icon in the
library (`the server` for an icon added from a template file).

phenix reads the directory only when it starts. Restart `phenix ui` after
you add, change or remove a file. phenix keeps the collections in memory and
never writes them to the store. It writes to the store only the icons that
the library did not have.

phenix makes the ID of each collection and template from the name of the
file and the name of the template. The IDs stay the same from one start to
the next. When you rename a file, its collection gets a new ID.

## The builder-doc annotation

A topology names its diagram in the `builder-doc` annotation of its Topology
config. The annotation is a map with up to three keys:

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
```

| Key | What it holds |
|---|---|
| `digest` | The digest of the Builder document: `sha256:` and 64 lowercase hex digits. It names the published document with that content that phenix stores for this topology. With `path`, it also tells which content the file must hold. |
| `id` | The ID of a published document in the phenix store. It applies only to the topology that the document was published to. |
| `path` | The absolute path of a Builder file on the phenix server (see [Builder documents in files](#builder-documents-in-files)). |

Each key is optional, but the map must have at least one key, and no other
keys. **Publish** writes `digest` and `id`. You write `path` yourself. A
publish keeps a `path` that the topology already has.

`builder-doc` is the only annotation that is a map. Every other annotation
of a config is text. In the JSON of a config, it is an object:

```json
"annotations": {
  "builder-doc": {
    "digest": "sha256:1ff0029f39e932995bffdc1335c03683dc7ff94bead6b943b788431187d861db",
    "id": "87d5f9c5e686bdf96898b772f1b8f863b7d3c0f3e7ece332629a03f3849badcb"
  },
  "maintainer": "range-team",
  "purpose": "Water utility training range"
}
```

### Which diagram a topology shows

1. The published document in the store, when the annotation names one: by
   `id`, or by `digest` when there is no `id`. The document must have been
   published to this topology. When the annotation has a `digest`, the
   document must have that digest.
2. Otherwise, the Builder file at `path`, when the annotation has one. With
   a `digest`, the file must hold the document with that digest.
3. Otherwise, none. The topology is not listed under **Published
   Diagrams**. It still has the tag `builder` on the **Configs** page. Its
   tag and its edit button open the **Import** dialog, set to the topology,
   with "Topology pump-station has no Builder diagram to open. Import it to
   make one."

The stored document comes first because the stored topology was published
from it. The file may have changed since then.

### What phenix checks

phenix checks the annotation each time a Topology config is created or
updated, by any means: `phenix config create` and `phenix config edit`, the
**Configs** page, and the REST API. It refuses a config whose `builder-doc`
is not valid. With `phenix config create`, the log line before the error
gives the reason:

```console
$ phenix config create pump-station.topology.yaml
2026-10-01 21:52:35.595 ERR calling config hook: config validation failed: topology pump-station: builder: invalid request: builder-doc.path: must be an absolute path type=SYSTEM uuid=197b8a05-ccb3-4162-994a-c39d8ebc1dc5
Error: Unable to create configuration from pump-station.topology.yaml (search error logs for 197b8a05-ccb3-4162-994a-c39d8ebc1dc5)
```

The reasons are:

| Reason | What is wrong |
|---|---|
| `value must be an object` | `builder-doc` is text, not a map |
| `property "file" is unsupported` | A key other than `digest`, `id` and `path` |
| `there must be at least 1 properties` | The map is empty |
| `builder-doc: digest is not a sha256 digest` | `digest` is not `sha256:` and 64 lowercase hex digits |
| `builder-doc.path: must be an absolute path` | `path` does not start with `/` |
| `builder-doc.path: must be a clean path, with no ".", "..", "//" or trailing "/"` | `path` has one of these |
| `builder-doc.path: must end in .json, .yaml or .yml` | `path` has a different ending. phenix also refuses an ending in capitals, such as `.JSON`. |

A `path` can be at most 1024 bytes long. phenix does not look for the file
when it stores the config. It reads the file only when someone opens the
diagram.

`phenix config edit` changes the keys of an annotation, but cannot remove an
annotation. To remove `builder-doc` from a topology, send the config without
the annotation with `PUT /api/v1/configs/topology/<name>`. Or delete the
topology and create it again without the annotation.

### Renaming and copying a topology

A published document belongs to one topology name. A topology can be stored
under a different name: renamed, or its config copied and created under a
new name. Then the `id` and `digest` that a publish wrote no longer name a
document of that topology, so phenix removes them:

- Without a `path`, phenix removes the whole annotation. The topology is
  then a plain Topology config until it is published again.
- With a `path`, phenix removes the `id`. The `path` and the `digest` stay,
  so phenix reads the topology from the file. The file must still hold the
  document with that digest.

## Builder documents in files

A topology can name a Builder document that is a file on the phenix server,
instead of a published document in the store. Use this for topologies kept
in a repository that is checked out on the server. The Topology config and
its diagram are then files side by side, and the diagram opens in Builder
without a publish first.

### Example

The directory `/phenix/topologies/pump-station/` holds two files:

- `pump-station.builder.json`, the Builder document (the example file
  [pump-station.builder.json](examples/pump-station.builder.json)).
- `pump-station.topology.yaml`, the Topology config that the document
  publishes, with a `builder-doc` annotation that names the document:

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
            # ... the rest of the nodes
    ```

To make the Topology config, open the document in Builder and select
**Download** > **Topology YAML** (see
[Topology YAML](import-upload-download.md#topology-yaml)). Then set
`metadata.name` and add the annotation. Use the downloaded `spec` without
changes. When the topology differs from what the document publishes,
Builder says so, and a draft made from the file cannot update the topology
(see
[Publishing to a topology that names a Builder file](publishing.md#publishing-to-a-topology-that-names-a-builder-file)).

Do not use the example file
[pump-station.topology.yaml](examples/pump-station.topology.yaml) for this,
although it has the same name. It is the config that the diagram was
imported from. It lists its nodes in a different order than the document
publishes them, so Builder says that the topology differs from the file.

Store the config:

```console
$ phenix config create /phenix/topologies/pump-station/pump-station.topology.yaml
2026-10-01 21:52:35.788 INF configuration created type=SYSTEM kind=Topology name=pump-station
```

The **Published Diagrams** tab now lists `pump-station` with the tag
**File** and "Read from
/phenix/topologies/pump-station/pump-station.builder.json" (see
[Diagrams read from a file](drafts.md#diagrams-read-from-a-file)). **Open**
shows the diagram, read only.

After a `git pull` changes the Builder file, the next **Open** shows the new
diagram: phenix reads the file each time, and keeps no copy. The stored
Topology config does not change with the file. To make the topology match
the file again, create the config again, or publish a draft made from the
file.

### Which files phenix reads

phenix reads a Builder file only when all of these conditions are true:

- The file is below the phenix base directory: `/phenix`, or the directory
  that the `base-dir.phenix` setting names (see [Settings](../settings.md)).
- It is not below the directory where VM file systems are mounted:
  `/phenix/mounts`, or the directory that the `mount-dir` setting names.
- It is a regular file, not a directory, a device or a pipe. phenix follows
  a symbolic link only when its target is a relative path that stays below
  the base directory and outside the mount directory. phenix refuses a link
  with an absolute target, such as the link that
  `ln -s /phenix/topologies/a.json b.json` makes, even when the target is
  below the base directory.
- It is at most 5 MiB.
- It holds one valid Builder document, as JSON or YAML. The content
  decides which, not the file name. A YAML file must not use anchors,
  aliases, merge keys or more than one document.

phenix fills in `${NAME}` in the `path` of a config from the environment
when the config is created, as everywhere in a config. `${NAME}` in the
Builder file stays as it is.

phenix resolves the path on the server that runs `phenix ui`. When several
phenix servers share one store, each server reads its own file system.

phenix reads the file when someone opens the diagram, makes a draft from it,
or publishes such a draft to the topology. The **Published Diagrams** tab
reads no file when it lists the diagrams, so it lists a card even when its
file is missing or not valid. Creating, editing, renaming and deleting the
config read no file either, and no `phenix` command reads one.

### Who can read a Builder file

Anyone whose role has `configs` `get` on the topology can open its diagram.
The `path` is part of the config, so everyone who can list the config can
see it.

!!! warning
    The name of the topology that names a Builder file does not protect the
    file. A user whose role has `configs` `create` or `update` on any one
    topology name, and `get` on that name, can store a topology there whose
    `path` names any Builder file below the base directory. Then the user
    can open the diagram of that file.

    For example, a role limited to `Topology/teama-*` keeps its users from
    the topologies and published diagrams of team B. It does not keep them
    from a Builder file of team B: they store `teama-x` with the `path` of
    that file and open the diagram of `teama-x`. They need the path of the
    file, which a fixed layout such as `/phenix/topologies/<name>/` makes
    easy to guess.

    Keep a Builder file below the base directory only when every user who
    can create or update a topology is allowed to read it. Published
    diagrams do not have this gap: a published document belongs to the
    topology that it was published to.

### Pinning the file with a digest

With `path` alone, the topology shows the document that the file holds,
whatever it is. Add `digest` to accept one document only:

```yaml
    builder-doc:
      digest: sha256:1c92d3b0c95588fbb20926c1e58bb0452903c4e2cb0ffbf12fcdc19f72717732
      path: /phenix/topologies/pump-station/pump-station.builder.json
```

When the file holds a different document, the diagram does not open:
"Builder file /phenix/topologies/pump-station/pump-station.builder.json does
not match the digest topology pump-station records for it."

The digest is the SHA-256 of the document as phenix writes it, not of the
file. `sha256sum pump-station.builder.yaml` gives a different value. A YAML
file, or a JSON file with different spacing, key order or final line break,
has different bytes from the document that phenix writes. To get the
digest, use the REST API for a topology that already names the file (see
[Examples](#examples)).

### When the file cannot be used

Opening the diagram then fails with "Could not open the diagram of topology
pump-station." and one of these sentences. Each sentence names the path,
and tells nothing of what the file holds. The REST API answers with the
same sentence as `message`.

| Message | Status | Why |
|---|---|---|
| "Builder file … is outside /phenix, the directory phenix reads Builder files from." | 422 | The path is not below the base directory, or it is below the mount directory |
| "Builder file … does not exist on this phenix server." | 404 | No file is at the path, also when a part of the path is a file and not a directory |
| "Builder file … cannot be read by phenix." | 422 | phenix has no permission to read it, a symbolic link leaves the base directory or has an absolute target, or reading failed |
| "Builder file … is not a regular file." | 422 | The path is a directory, a device or a pipe |
| "Builder file … is larger than 5 MiB." | 413 | The file is too large |
| "Builder file … is not a valid Builder document. Upload it in the Builder to see why." | 422 | The file is not JSON or YAML, or not a valid Builder document. **Upload** the file to see what is wrong with it (see [Uploading a Builder document](import-upload-download.md#uploading-a-builder-document)) |
| "Builder file … does not match the digest topology pump-station records for it." | 422 | The annotation has a `digest`, and the file holds a different document |

The phenix log has a line for each failure: `builder document file not
usable`, with the topology, the path and the reason.

### Editing and publishing

**Edit as a draft** on the diagram makes a draft from the file, as it does
from a published diagram (see
[Diagrams read from a file](drafts.md#diagrams-read-from-a-file)). For when
that draft can update the topology, and for the warning that Publish shows,
see
[Publishing to a topology that names a Builder file](publishing.md#publishing-to-a-topology-that-names-a-builder-file).

**Publish never writes the file.** It stores the diagram as a published
document, as every publish does, and writes `digest` and `id` beside the
`path`:

```yaml
    builder-doc:
      digest: sha256:4ba9ae46494bb7d2498f5119b55996f332c52c1b95d79502361afbf50707cbc3
      id: 89f0a9779c2e5892da9f7a9ae2ed5c58c564fe57ea86f50f4452f174cb68ad93
      path: /phenix/topologies/pump-station/pump-station.builder.json
```

From then on, the topology shows the published document, and its
**Published Diagrams** card has no **File** tag. The file is then out of
date until you replace it.

The `path` stays because it is still useful where the published document is
missing. On a different phenix server that gets this Topology config,
phenix reads the diagram from the file there, but only when the file holds
the document with that `digest`.

## REST API

Scripts use the REST API of the Builder, which the web UI also uses. All
routes are under `/api/v1`. The interactive API docs of a running server, at
`/docs/`, describe each request and response under the **Builder** tag (see
[Interactive API Docs](../api.md#interactive-api-docs-swaggeropenapi)).

The REST API has no single request that publishes a Builder file. Create a
draft from the document, then publish the draft.

Each route needs the `configs` permission in its row. The request must also
pass the checks in [Permissions](#permissions). For the draft of another
user, it needs a share or `builder-drafts`. For Import and Publish, it needs
the permissions of each config.

| Route | What it does | `configs` permission |
|---|---|---|
| `GET /schemas/builder/v1` | The JSON Schema of the Builder document | None: `schemas` `get` on `builder` |
| `GET /schemas/builder/templates/v1` | The JSON Schema of a template file | None: `schemas` `get` on `builder` |
| `GET /schemas/builder/package/v1` | The JSON Schema of a Builder package | None: `schemas` `get` on `builder` |
| `GET /builder/drafts` | List your drafts (`drafts`), the drafts of other users that you can see (`shared`), and the drafts that this server cannot read (`damaged`) | `list` |
| `POST /builder/drafts` | Create a draft | `create` |
| `GET /builder/drafts/{owner}/{draft}` | Read a draft, with its current document | `get` |
| `DELETE /builder/drafts/{owner}/{draft}` | Delete a draft | `delete` |
| `GET /builder/drafts/{owner}/{draft}/snapshots` | List the snapshots of a draft | `get` |
| `POST /builder/drafts/{owner}/{draft}/snapshots` | Save a new version of the document | `update` |
| `GET /builder/drafts/{owner}/{draft}/snapshots/{snapshot}` | Read the document of one snapshot (`current` for the current one) | `get` |
| `DELETE /builder/drafts/{owner}/{draft}/snapshots/{snapshot}` | Delete a snapshot other than the current one | `update` |
| `PATCH` or `PUT /builder/drafts/{owner}/{draft}/cursor` | Undo and redo: choose the current snapshot | `update` |
| `POST /builder/drafts/{owner}/{draft}/publish` | Create or update the Topology and Experiment configs, and add the topology to the `topology` annotation of each scenario that the document lists. With `"dryRun": true`, tell what publishing would change, and write nothing. A dry run needs no `If-Match` | `update`, and the permissions of each config |
| `POST /builder/drafts/{owner}/{draft}/preflight` | Run the [preflight checks](editor.md#preflight) named in `checks` (`capacity`, `network`, `disks`, `apps`) on the current document of the draft, against the `experiment` named, if any (writes nothing) | `get`, and the permissions of each check (see [What each task needs](#what-each-task-needs)) |
| `GET`, `PUT /builder/drafts/{owner}/{draft}/shares` | Read or replace who a draft is shared with (owner only) | `get`, `update` |
| `GET /builder/drafts/{owner}/{draft}/shares/candidates` | The users that a draft can be shared with | `update` |
| `GET /builder/sources` | The configs that a document can be made from or published with | `list` |
| `POST /builder/generate` | Make a document from a stored Topology or Experiment config, or from a config file, with the [import options](import-upload-download.md#import-options) (`includes`, `copy`, `name`) | `get`. `create` for a config file |
| `POST /builder/legacy` | Convert a [legacy Builder](legacy.md) diagram, or a Topology config file that holds one, into a document (writes nothing) | `get` and `create` |
| `POST /builder/export/topology` | The Topology YAML that a document would publish (writes nothing) | `get` |
| `POST /builder/package` | The [Builder package](import-upload-download.md#moving-a-diagram-with-a-builder-package) of a document, with the sections that `include` names (writes nothing) | `get`. For each config that it carries, `get` with `scenarios` or `topologies` `list` |
| `POST /builder/package/resolve` | Which of the items that the diagram of a package needs this server has: `present`, `missing`, `different` or `unknown` (writes nothing) | `get`. `list` for icons, `disks` `list` for disk images, `applications` `list` for apps |
| `GET /builder/documents` | List the published documents (`source` `store`), and the topologies that name a Builder file instead (`source` `file`, with the `path` and no `id`) | `list` |
| `GET /builder/documents/{document}` | Read a published document | `get` on its topology |
| `DELETE /builder/documents/{document}` | Delete a published topology and its published documents | `delete` on the topology |
| `GET /builder/topologies/{topology}/document` | Read the diagram that a topology names in `builder-doc`, from the store or from its Builder file | `get` on the topology |
| `GET /builder/icons` | List the icon library of the server, with the number of icons and bytes that you uploaded | `list` |
| `POST /builder/icons` | Upload a PNG under a name | `create` |
| `GET /builder/icons/{icon}` | Read an icon, by its name or by a different name of it | `get` |
| `PUT /builder/icons/{icon}` | Rename an icon. The old name still names it | `update`. For the icon of another user, also `builder-icons` `update` |
| `DELETE /builder/icons/{icon}` | Delete an icon and all its names | `delete`. For the icon of another user, also `builder-icons` `delete` |
| `GET /builder/templates` | List the templates and collections that you can use: yours, those shared with you, the server-wide ones, and the server collections (`preloaded`) | `list` |
| `GET /builder/templates/candidates` | The users that your templates can be shared with | `update`, and authentication enabled |
| `POST /builder/templates/{owner}/items` | Add templates to your library | `create` |
| `PUT /builder/templates/{owner}/items/{template}` | Replace a template of your library | `update` |
| `POST /builder/templates/{owner}/collections` | Add a collection to your library | `create` |
| `PUT /builder/templates/{owner}/collections/{collection}` | Replace a collection of your library | `update` |
| `POST /builder/templates/{owner}/delete` | Delete templates and collections of your library | `delete` |
| `POST /builder/templates/{owner}/restore` | Restore deleted built-in templates to your library | `create` |
| `POST /builder/templates/{owner}/share` | Add or remove the users that your templates and collections are shared with | `update`, and authentication enabled |
| `POST /builder/templates/{owner}/publish` | Publish templates and collections server-wide, or take them back | `update`. To publish, and to take back the item of another user, also `builder-templates` `publish` |

`{owner}` in a template route is your own user name. Only its owner changes
a library, and any other name answers 404. The one exception is to take back
the server-wide item of another user, with `builder-templates` `publish`. A
template route that names a template or a collection of a server collection
answers 409, because server collections are read only.

With authentication enabled, send a token in the `X-Phenix-Auth-Token`
header, as `Bearer <token>` (see
[Generating User Authentication Tokens](../user-administration.md#generating-user-authentication-tokens)).
With authentication disabled, no header is necessary.

Each change to an existing draft needs an `If-Match` header with the current
ETag of the draft, as the last response returned it. A request without one
answers 400. A request with an older ETag answers 412: read the draft again,
and send the request again with the new ETag. Draft responses also have the
ETag in their body, as `etag`. Use that value, because a proxy that
compresses responses can change the `ETag` header.

A refused request answers with a JSON body whose `code` names the failure,
such as `publish.topology.exists` or `request.stale`. When a document does
not validate, or only publishing refuses a draft, the body also lists each
problem in `issues`, each with its own code. The warnings and failures of a
publication are issues too. [Error Codes](error-codes.md) lists every code.

### Response headers

Each response of a Builder route has the header
`X-Content-Type-Options: nosniff`, because each response is JSON. The schema
routes of the document, the template file and the package have it too. The
responses of the icon routes hold the uploaded images as base64 in the JSON.
They also have the header
`Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`. If a
browser shows one of them as a page, the browser loads, runs and frames
nothing. For a proxy that adds a policy to the application page, see
[Availability](#availability).

### Examples

These examples use the example lab (see
[The example lab](index.md#the-example-lab)) and `jq`. Set the server and
your token first:

```bash
PHENIX=http://localhost:3000
TOKEN='your-token'   # a token from the Users page
```

List your drafts:

```bash
curl -s -H "X-Phenix-Auth-Token: Bearer $TOKEN" "$PHENIX/api/v1/builder/drafts" \
  | jq '.drafts[] | {title, owner, id, etag}'
```

```json
{
  "title": "Metro Campus",
  "owner": "e2e-admin",
  "id": "4f049bec-cf47-4fbb-979d-8baea1aa033f",
  "etag": "\"6\""
}
{
  "title": "Riverside Water",
  "owner": "e2e-admin",
  "id": "6290aef3-24a3-4126-b90a-308cbf561f17",
  "etag": "\"10\""
}
{
  "title": "Riverside Water expansion",
  "owner": "e2e-admin",
  "id": "829f7ef6-f8e8-48b1-b1a3-e744a29c6561",
  "etag": "\"4\""
}
```

Import the stored topology `riverside-water` as a new draft, as **Import**
does. `POST /builder/generate` makes the document and writes nothing.
`POST /builder/drafts` stores it as a draft:

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
    "Added 2 nodes from included topology corp-services (2 nodes). They are shown read only: edit them in their own topology. Publishing keeps includeTopologies instead of copying them."
  ]
}
```

```bash
jq '{title: .document.metadata.name, sourceToken: .source.fullName, document}' generated.json \
  | curl -s -X POST -H "X-Phenix-Auth-Token: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d @- "$PHENIX/api/v1/builder/drafts" > created.json
jq '{id, owner, title, etag}' created.json
```

```json
{
  "id": "6b471c7d-dd32-44e7-ae85-a889f377747f",
  "owner": "e2e-admin",
  "title": "riverside-water",
  "etag": "\"14\""
}
```

The `sourceToken` connects the draft to the topology that it came from, so
the draft can later publish back to `riverside-water`.

Convert a diagram of the [legacy Builder](legacy.md), as **Upload** does.
`content` is the text of the file: the XML of the diagram, or a Topology
config that holds it in `builder-xml`. `name` names a diagram that comes
without a topology. Nothing is written:

```bash
jq -Rs '{content: ., name: "plant"}' plant.xml \
  | curl -s -X POST -H "X-Phenix-Auth-Token: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d @- "$PHENIX/api/v1/builder/legacy" > converted.json
jq '{name: .document.metadata.name, warnings}' converted.json
```

The answer has the same form as the answer of `POST /builder/generate`.
Create a draft from its `document` as above.

The server writes who made and last saved the diagram into the `metadata` of
the document that it stores (see
[Who made and last saved a diagram](import-upload-download.md#who-made-and-last-saved-a-diagram)).
The answers to creating and to saving a draft have no document, so they
return these four values as `stamp`:

```bash
jq .stamp created.json
```

```json
{
  "createdBy": "e2e-admin",
  "createdAt": "2026-10-02T03:53:41Z",
  "updatedBy": "e2e-admin",
  "updatedAt": "2026-10-02T03:53:41Z"
}
```

A draft made from an uploaded file can carry the name of the file, which the
Inspector shows as **Source file**. Send it as `sourceFile` beside
`document`, for example `"sourceFile": "riverside-water.builder.json"`. It
must be a plain file name of at most 255 bytes, with no `/` or `\`.

Delete that draft with its current ETag. The owner and ID come from
`created.json`. The owner is your user name, or `global-admin` when
authentication is disabled:

```bash
OWNER=$(jq -r .owner created.json)
ID=$(jq -r .id created.json)
ETAG=$(curl -s -H "X-Phenix-Auth-Token: Bearer $TOKEN" "$PHENIX/api/v1/builder/drafts/$OWNER/$ID" | jq -r .etag)
curl -s -X DELETE -H "X-Phenix-Auth-Token: Bearer $TOKEN" -H "If-Match: $ETAG" \
  "$PHENIX/api/v1/builder/drafts/$OWNER/$ID"
```

The delete answers 204 with no body. Without `If-Match`, it answers:

```json
{"cause":"","message":"an If-Match header is required for this request"}
```

Read the diagram of a topology, from the store or from a file. This example
reads the topology `pump-station` of
[Builder documents in files](#builder-documents-in-files). `jq` leaves out
the document itself:

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

`digest` is the digest to write beside `path` (see
[Pinning the file with a digest](#pinning-the-file-with-a-digest)). For a
file, the answer also has `"topologyDiffers": true` when the stored topology
is not what the document publishes. For a published document, `source` is
`store`. The answer then has the `id` of the document, and who published it
and when (`createdBy`, `createdAt`), instead of a `path`.

## Compressed files

The Builder page loads large script files that the other pages do not
load. The UI build writes a Brotli copy and a gzip copy of each of these
files when the copy is smaller than the file. phenix sends the copy that the
browser accepts, and tells the browser to keep the file for one year. A file
gets a new name when its content changes, so browsers download changed files
again after an upgrade.

The environment variable `PHENIX_BROTLI_QUALITY` sets the Brotli quality of
the UI build. Use a whole number from 0 to 11. The default is 9. A higher
quality makes smaller copies, but the build takes more time. The build
stops with an error when the value is not a whole number from 0 to 11.

The Docker image (`docker/Dockerfile`) and the Podman image
(`podman/Containerfile`) set quality 11. These builds use those images:

- `make docker` builds `docker/Dockerfile`.
- `make deb` builds `docker/Dockerfile`, and then makes the package from that
  image. When you set `PHENIX_BUILD_IMAGE` to an image that you built before,
  `make deb` uses that image. The package then has the quality that the image
  used.

All other builds use quality 9, for example `make build` and
`npm run build`. To build the UI with quality 11, as the packages do:

```bash
cd src/js
PHENIX_BROTLI_QUALITY=11 npm run build
```

To build the Docker image with a different quality, give it as a build
argument:

```bash
docker build --build-arg PHENIX_BROTLI_QUALITY=9 -t phenix -f docker/Dockerfile .
```

## Troubleshooting

| What you see | Why | What to do |
|---|---|---|
| No **Builder** tab, a button that is missing or unavailable, "Your role can open drafts and published diagrams, but not create drafts.", "Could not load this server's form fields. …" or "Drive images are not checked: …" | The role of the user does not allow that task. **Publish** is also unavailable on a read-only draft: a draft shared with **Can view**, or a published diagram | See [What users without a permission see](#what-users-without-a-permission-see) for the permission to give the role |
| "Could not publish the diagram. Adding topology riverside-water-b to scenario riverside-water not allowed for …" | The diagram lists that scenario, and the role cannot update the scenario to add the topology | Give the role `configs` `update` on `Scenario/riverside-water`, or remove the scenario from the diagram (see [What each task needs](#what-each-task-needs)) |
| "Could not publish the diagram. Scenario riverside-water does not exist." | The diagram lists a scenario that the server does not have, or that the role cannot read | Store the scenario, give the role `configs` `get` and `scenarios` `list` on it, or remove it from the diagram (see [Scenarios](diagrams.md#scenarios)) |
| The Publish dialog says that the topology "already exists, and this diagram cannot update it" | The draft was not imported from that topology, opened from its published diagram, or published to it | Publish under a different name, or import the topology and make your changes in that draft (see [Publishing](publishing.md)) |
| The Publish dialog says that the topology "changed after this diagram published it" | Someone changed the topology after this draft published it | Import the topology again, or publish under a different name (see [Publishing again](publishing.md#publishing-again)) |
| The Publish dialog says that a topology the legacy Builder saved "already exists, and this diagram cannot update it" | Only the draft imported from that topology can replace its legacy diagram | Import the topology and publish that draft (see [Legacy Builder](legacy.md#converting-a-stored-topology)), or publish under a different name |
| "Could not convert the legacy diagram. …" | The file is not a diagram that the legacy Builder saved, or it is a Topology config without `builder-xml` | See [Converting a file](legacy.md#converting-a-file) |
| "Could not open the diagram of topology pump-station. Builder file … " | The topology names a Builder file that phenix cannot use | See [When the file cannot be used](#when-the-file-cannot-be-used) |
| "Topology … has no Builder diagram, and your role cannot create drafts to import it. Select its name in Configs to view it." | A link from the **Configs** page named a topology without a diagram, and the role has no `configs` `create` | Give the role `configs` `create`, or view the topology on the **Configs** page (see [Which diagram a topology shows](#which-diagram-a-topology-shows)) |
| "Topology … does not exist, or you may not read it." | The topology was deleted, or the role cannot list it | Give the role `configs` `list` and `topologies` `list` on the topology |
| `phenix config create` skips a file with "skipped Builder document; upload it in the Builder to publish it", or refuses it as "a Builder document, not a configuration" | The file is a Builder document, not a config | Upload it in the Builder and publish the draft (see [Builder documents and phenix config create](import-upload-download.md#builder-documents-and-phenix-config-create)) |
| **Copy link** shows a **Link to this draft** field and "Press ⌘C to copy the link." (Ctrl+C on Windows and Linux) | The page uses plain HTTP, where the browser does not allow the clipboard | Copy the selected link, or serve phenix over HTTPS (see [Availability](#availability)) |
| The save state says "Offline: …" | The browser cannot reach the server | Keep the tab open. Saving tries again when the server answers (see [Working offline](drafts.md#working-offline)) |
| "This draft changed on the server" | Someone saved a newer version of the draft before your changes reached the server | Choose how to keep your changes (see [When the draft changed on the server](drafts.md#when-the-draft-changed-on-the-server)) |
| The save state says "Could not save your changes. Etcd is out of space: …" | etcd reached its space quota | Free space in etcd (see [etcd](#etcd)). The editor tries again automatically |
| The **Sign in again** dialog | The session of the user ended (the token lifetime, `--jwt-lifetime`, is 24 hours by default) | Enter the password. Builder sends the changes that are not yet saved after sign-in (see [Signing in again](drafts.md#signing-in-again)) |
| A draft card says "This draft cannot be read, so it cannot be opened. A newer version of phenix may have saved it." | A newer phenix saved the draft | Open it with that phenix version, or delete it (see [Damaged drafts](drafts.md#damaged-drafts)) |
| "Auto layout failed. The layout engine could not start." or "Auto layout failed. The layout engine could not be loaded. Reload the page to try again.", or "The pattern could not be checked. Reload the page to try again." in **Auto-group by name pattern** | The browser could not start the layout worker or the pattern worker. These are scripts that phenix serves from its own address. A proxy or a content security policy may block them | Reload the page. Let the proxy serve the scripts of phenix, and let the content security policy allow workers from the same origin (see [Availability](#availability)) |
| "This image could not be converted. Save it as a PNG and upload it again." | The browser could not change the file into a PNG icon, for example an SVG that draws nothing on its own | Save the picture as a PNG and upload that |
| "Your library cannot be read. A newer version of phenix may have saved it." on the **Node Templates** tab | A newer phenix saved the template library of the user | Use that phenix version. The templates that other users share are still listed |
