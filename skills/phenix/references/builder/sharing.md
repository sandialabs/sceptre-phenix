# Access, sharing and RBAC

Part of the [Builder references](../builder.md). Draft shares, cross-user
access, the `configs` permission every request needs, ETags on mutations,
the `builder-drafts`, `builder-templates` and `builder-icons` RBAC
resources, and the built-in Builder role.

## Shares

Draft owners can manage their own drafts. An owner can share a draft with
named users as `view` or `edit`. `GET /builder/drafts/{owner}/{draft}/shares`
lists them (owner only). `PUT` of the same path with `If-Match: "shares-N"` and
`{"shares":[{"user":"bob","access":"edit"}]}` replaces the list: at most 25
users, and unknown users, the owner and duplicates get `422` with per-user
`errors`. A share gives view (`get`/`list`) or edit (also save, undo and
publish under the recipient's own permissions). It never gives delete or
sharing. Recipients still need their `configs` permissions. A share is bound
to the recipient's account, so a user deleted and recreated under the same
name loses it. `GET /builder/drafts/{owner}/{draft}/shares/candidates` lists
who the draft can be shared with, as
`{"users":[{"username":"alice","name":"Alice Tester"}]}` sorted by username
(`name` is the first and last name, or `""`). It holds every account that can
receive a share, whatever the caller's `users` permissions: it leaves out the
owner and anyone a `PUT` would refuse, and lists users already shared with. It
has the same access rules as `PUT .../shares`. The Share
dialog offers these users in a drop-down that filters as the user types, shown
as "Name (username)" and without those already listed. If the list cannot be
read, the dialog shows the error with Retry, and a typed username can still be
added (the server checks it on save). Cross-user access for administrators uses the
`builder-drafts` RBAC resource with `{owner}/{draft-id}` resource names. A role
that may inspect and modify every draft needs an explicit policy like:

```yaml
- resources: [builder-drafts]
  resourceNames: ["*/*"]
  verbs: [list, get, update, delete]
```

`create` is deliberately not a `builder-drafts` verb: a draft is always created
for the authenticated user, never on somebody else's behalf. Resource names are
matched with `filepath.Match`, which does not match `/`, so a bare `"*"` never
matches a `{owner}/{draft-id}` name — use `"*/*"` (as `global-admin` does) or an
explicit `alice/*`.

## Permissions, ETags and draft responses

Every Builder request also needs the base `configs` permission of the verb
it performs (`list`, `get`, `create`, `update`, `delete`), so builder access can
never exceed a user's config access. A caller who cannot see another user's
draft (no share, no `builder-drafts` permission) gets `404`, so draft existence
is never disclosed. A caller who can see it but may not perform the operation
(a viewer saving, an editor deleting or sharing) gets `403`. Every mutation
after creation requires an `If-Match` header carrying the quoted ETag the
previous response returned: a missing or malformed tag is `400`, a stale one
`412`. Draft responses also carry the tag in their body as `etag`; prefer it,
since a compressing proxy can rewrite the header (`W/"3"`, `"3-gzip"`). Draft
responses carry `access` (`owner`/`edit`/`view`), `via` (`share`/`role`) for
other users' drafts, and, for the owner, `canShare` and `shares`.
`GET /builder/drafts` lists other users' drafts the caller may see in `shared`,
those shared with the caller with `via: "share"`. Changing shares changes the
draft's ETag; the share list has its own `"shares-N"` tag. A draft that has
ever been shared shows as damaged (its owner can delete it) on a phenix
version without sharing. `GET /builder/drafts` also returns `damaged`: drafts
whose metadata this server can no longer read (written by a newer phenix, say),
with `id`, `owner`, `etag`, `canDelete`, and `title` and `updated` when they
can be read. They cannot be opened, only deleted with that `etag`; a `412` on
`DELETE` also carries the current `ETag`.

Publishing still requires the applicable config, scenario, and experiment
permissions; Builder draft access does not bypass them.

`GET /builder/drafts` rows and draft responses carry `canDelete` (computed
on every read, never stored): another user's draft has it when the role
holds `builder-drafts` `delete` and `configs` `delete`, and the drafts page
then offers Delete on its card. Draft responses (one draft, `POST
/builder/drafts`, the draft in a publish answer; never the listing) carry
`experiment`, and stored rows of `GET /builder/documents` too: the name of
an experiment the publication made that still exists, read from its
`builder-experiment` annotation on every request, only for a caller with
`experiments` `get` on it (one is named: the draft's last publication's,
then one recording the document, then the first by name).

## Template and icon permissions

`builder-templates` `publish` (no resource names; a literal check,
`builderTemplatesPublishAllowed` in `web/builder.go`, so `make generate`
records it) lets a role publish template library items to every user and
take any user's server-wide item back. The template library is the caller's
own: no role, also not Global Admin or `builder-drafts`, reads or changes
another user's (404), except taking back a server-wide item.

`builder-icons` `update` and `delete` (no resource names; literal checks,
`builderIconsUpdateAllowed` and `builderIconsDeleteAllowed` in
`web/builder.go`) let a role rename and delete icons of the shared icon
library that other users uploaded. Every caller with `configs` `list` reads
the whole icon library; the uploader of an icon renames and deletes it with
`configs` `update` and `delete`.

## The built-in Builder role

The built-in role `Builder` (`api/config/default/builder.yml`,
`metadata.name: builder`) holds every Builder permission: `configs` all five
verbs on `Topology/*`, `Scenario/*`, `Experiment/*` only (never `*/*`, which
would expose User and Role configs: password hashes and role changes),
`builder-drafts` `list`/`get`/`update`/`delete` on
`*`/`*/*`, `builder-templates` `publish`, `builder-icons` `update`/`delete`,
`schemas` `get`, `topologies` and `scenarios` `list`/`get`, `experiments`
`list`/`get`/`create`/`update`, `disks` `list`.
`rbac.EnsureBuilderRolePermissions` (called from `web.Init` at every start)
creates it when no role is named `builder` or has role name `Builder` (so old
stores get it, and a deleted one comes back), and adds the
`builder-templates` `publish` and `builder-icons` `update`/`delete` grants it
lacks to an existing role of that name and to the users assigned it,
changing nothing else. Of the other built-in roles only Global Admin can
publish templates or rename and delete other users' icons (`*`). The example
roles `docs/content/builder/examples/roles/topology-*.role.yaml` are for
sites that want less.
