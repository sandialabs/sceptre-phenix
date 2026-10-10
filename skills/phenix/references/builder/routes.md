# Routes

Part of the [Builder references](../builder.md). Every Builder REST route
with its purpose and permissions; the reference for each area has the
contract in full.

All routes are relative to `/api/v1`.

| Route | Purpose |
|---|---|
| `GET /schemas/builder/v1` | JSON Schema of the Builder document (`builder/v1`); needs `schemas` `get` on the resource name `builder` |
| `GET /schemas/builder/templates/v1` | JSON Schema of a template file; same permission |
| `GET /schemas/builder/package/v1` | JSON Schema of a Builder package; same permission |
| `GET/POST /builder/drafts` | List the caller's drafts (`drafts`), other users' drafts the caller may see (`shared`) and unreadable drafts (`damaged`); create a draft (optionally `forkOf` or `sourceToken`) |
| `GET/DELETE /builder/drafts/{owner}/{draft}` | Read a draft with its current document; delete it |
| `GET/POST /builder/drafts/{owner}/{draft}/snapshots` | List or append snapshots (append needs `If-Match`) |
| `GET /builder/drafts/{owner}/{draft}/snapshots/{snapshot\|current}` | Read one snapshot's document |
| `DELETE /builder/drafts/{owner}/{draft}/snapshots/{snapshot}` | Delete a version other than the current one (needs `If-Match`) |
| `PATCH/PUT /builder/drafts/{owner}/{draft}/cursor` | Undo and redo: move the draft's current snapshot |
| `POST /builder/drafts/{owner}/{draft}/publish` | Create or update the topology and experiment configs, and add the topology to the document's scenarios; with `dryRun`, say what that would change and write nothing |
| `POST /builder/drafts/{owner}/{draft}/preflight` | Check the current snapshot's host capacity, networks, disk images and scenario apps against the server and its cluster; nothing is written (see [Preflight checks](preflight.md)) |
| `GET/PUT /builder/drafts/{owner}/{draft}/shares` | Read or replace who a draft is shared with (owner only) |
| `GET /builder/drafts/{owner}/{draft}/shares/candidates` | Every account that can receive a share of the draft |
| `GET /builder/sources` | Configs a document can be generated from or publish to; topology rows have `includeCount` |
| `POST /builder/generate` | Build a document from a stored or uploaded Topology or Experiment, with `includes`, `copy`, `name`; converts a `builder-xml` topology (`configs` `get`; `create` for `content`) |
| `POST /builder/legacy` | Convert a legacy diagram or a Topology file with `builder-xml` into a document (`configs` `get` and `create`; nothing is written) |
| `POST /builder/export/topology` | The Topology config a document publishes as, as YAML, with `warnings` and `publishBlockers` (nothing is written; needs `configs` `get`) |
| `POST /builder/package` | The Builder package of a document, with the sections `include` names, and `warnings` (nothing is written; `configs` `get`) |
| `POST /builder/package/resolve` | Which of what a package's diagram needs this server has: `present`, `missing`, `different`, `unknown` (nothing is written; `configs` `get`) |
| `GET /builder/documents[/{document}]` | Published Builder documents (`source: "store"`); the listing also has a row per topology read from a Builder file (`source: "file"`) |
| `DELETE /builder/documents/{document}` | Delete the topology a published document is current for, and the topology's published documents |
| `GET /builder/topologies/{topology}/document` | The document a topology's `builder-doc` names, stored or read from its Builder file: the listing row plus `digest`, `size`, `document`, and for a file `topologyDiffers` |
| `GET/POST /builder/icons` | The server's icon library: list every icon, upload one under a name (`configs` `list`, `create`) |
| `GET/PUT/DELETE /builder/icons/{icon}` | Read, rename (the old name stays an alias), delete an icon by name or alias (`configs` `get`, `update`, `delete`; another user's icon also needs `builder-icons` `update` or `delete`) |
| `GET /builder/templates` | Templates and collections the caller can use: own (`source` `own`), shared (`shared`), server-wide (`server`), with `canShare`, `canPublish`, `damaged`, `limits`, and the server collections in `preloaded` (`configs` `list`) |
| `GET /builder/templates/candidates` | Accounts the caller's items can be shared with (`configs` `update`, a user account) |
| `POST /builder/templates/{owner}/items`, `PUT …/items/{template}` | Add templates (optionally as a new `collection`), replace one (`If-Match`) (`configs` `create`, `update`; owner only) |
| `POST /builder/templates/{owner}/collections`, `PUT …/collections/{collection}` | Add, replace a collection (`If-Match` on PUT) (`configs` `create`, `update`; owner only) |
| `POST /builder/templates/{owner}/delete` | Delete templates and collections (`configs` `delete`; owner only) |
| `POST /builder/templates/{owner}/share` | Add or remove users of items (`configs` `update`, owner, a user account) |
| `POST /builder/templates/{owner}/publish` | `serverWide` true publishes the owner's items (`configs` `update` and `builder-templates` `publish`); false takes them back (owner, or anyone with `builder-templates` `publish`) |

The OpenAPI document served at `/docs/` describes every request and response.
