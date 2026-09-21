# Topology Builder Reference

Detailed reference for phēnix's graphical topology Builder. Load this when
creating, editing, translating, or debugging Builder diagrams. For everything
else, `../SKILL.md` is sufficient.

Builder is the graphical topology editor served at `/builder`. It is a
customized JGraph/draw.io GraphEditor backed by an **mxGraph XML model**. The
diagram XML preserves layout, icons, labels, and per-cell configuration;
Builder separately derives a phēnix `Topology` config and experiment VLAN
aliases from the cells' `schemaVars` JSON.

## Builder API

Authenticate API requests as described in SKILL.md's "Querying the Web API".
`GET /builder` and `POST /builder/save` sit at the server root; the topology
endpoints use the `/api/v1` base path.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/builder` | Load the graphical editor (server root, not `/api/v1`) |
| `POST` | `/builder/save` | Download XML/SVG (server root, not `/api/v1`) |
| `GET` | `/api/v1/builder/topologies` | List readable topology configs that contain Builder XML |
| `POST` | `/api/v1/builder/topologies` | Create a topology and its Builder XML without creating an experiment |
| `GET` | `/api/v1/builder/topologies/{name}` | Return one diagram as `application/xml` |
| `PUT` | `/api/v1/builder/topologies/{name}` | Update an existing topology and its Builder XML without touching an experiment |
| `POST` | `/api/v1/experiments/builder` | Create a topology and same-named experiment |
| `PUT` | `/api/v1/experiments/builder` | Update a Builder topology and its same-named experiment |

`POST /builder/save` takes form fields `filename`, `xml`, and an optional
`format` (`xml` or `svg`). The editor posts through a hidden form whose `xml`
field already holds an `encodeURIComponent()` result, so the server
percent-decodes that field once and then returns the document unchanged:
`format=xml` (the default) uses `application/xml`, `format=svg` uses
`image/svg+xml`. It does not render PNG, GIF, JPEG, or PDF, and rejects any
other `format` value.

### Payload

`POST`/`PUT /api/v1/experiments/builder` and the
`/api/v1/builder/topologies` endpoints share one payload shape. The topology
endpoints ignore `vlans` and `scenario`; `PUT` also ignores `name`, taking the
topology name from the URL instead:

```json
{
  "name": "branch-office",
  "topology": {
    "nodes": [
      {
        "type": "VirtualMachine",
        "general": {"hostname": "client-1", "vm_type": "kvm"},
        "hardware": {
          "os_type": "linux",
          "vcpus": 2,
          "memory": 2048,
          "drives": [{"image": "ubuntu.qc2"}]
        },
        "network": {
          "interfaces": [
            {
              "name": "eth0",
              "type": "ethernet",
              "proto": "static",
              "address": "10.10.10.10",
              "mask": 24,
              "gateway": "10.10.10.1",
              "vlan": "users"
            }
          ]
        }
      }
    ]
  },
  "vlans": {"users": 101},
  "scenario": "branch-office-apps",
  "builderXML": "<mxGraphModel>...</mxGraphModel>"
}
```

`scenario` may be empty. `name`, `topology`, and `builderXML` are all required;
requests missing any of them are rejected with 400 before the store is touched.
On `PUT /api/v1/experiments/builder`, the experiment's VLAN aliases are rebuilt
from the saved topology: aliases the request leaves out keep the ID they already
had — including ones phēnix allocated automatically — and aliases the topology
no longer references are dropped.

Use the Builder topology endpoints — not `/api/v1/configs` — to save a
topology from a diagram:

- `POST /api/v1/builder/topologies` creates one. The server owns the config's
  `apiVersion`, `kind`, and `builder-xml` annotation, so the caller sends only
  `name`, `topology`, and `builderXML`. It answers `201 Created` with a
  `Location` header naming the new diagram's `GET` URL, and `409 Conflict`
  while another Builder write holds the name.
- `PUT /api/v1/builder/topologies/{name}` updates one. It writes only the spec
  and the `builder-xml` annotation, leaving the rest of the stored metadata —
  other annotations, labels, creation time — intact, and answers
  `204 No Content`.

`PUT /api/v1/configs/topology/{name}` replaces the whole config body, so an
update through it drops every metadata annotation the caller did not send back.

Both endpoints refuse the write while an experiment created from that same
topology is running.

## Storing Builder XML

Builder identifies editable topologies by the `builder-xml` metadata
annotation, which holds the mxGraph document inline:

```yaml
metadata:
  name: branch-office
  annotations:
    builder-xml: |
      <mxGraphModel>
        ...
      </mxGraphModel>
```

`GET /api/v1/builder/topologies` lists exactly the topologies carrying this
annotation that the caller may read. A topology without it is still a valid,
deployable phēnix topology; it is just not editable in Builder.

## Diagram Model and Config Translation

An mxGraph document has an `<mxGraphModel>` root, an inner `<root>`, reserved
`mxCell` IDs `0` and `1`, and one cell per vertex or edge. Builder-specific
cells wrap `mxCell` in an `<object>` (or `<Object>`) carrying:

- `label`: displayed hostname or VLAN name.
- `schemaVars`: JSON containing phēnix semantics.
- `mxCell`: graph role (`vertex="1"` or `edge="1"`), style, source/target IDs,
  and an `mxGeometry` child.

When **View JSON / Save to phēnix** runs, Builder translates the graph as
follows:

1. Every vertex with `schemaVars` becomes a candidate.
2. Vertices whose `schemaVars.device` is `switch` represent VLANs and are not
   emitted as topology nodes.
3. Other semantic vertices become `spec.nodes[]`; Builder removes its private
   `device` and `schema` keys.
4. Node `annotations` and `labels`, represented in the editor as
   `[{key, value}]`, become phēnix maps.
5. A VLAN edge has `schemaVars` like `{"name":"users","id":101}`. Its
   source/target relationship causes matching network interfaces to be added
   to endpoint nodes.
6. Each nonzero edge VLAN ID becomes an experiment alias:
   `vlans: {"users": 101}`. On a VLAN edge's `schemaVars`, ID `0` means
   automatic allocation and is omitted from the submitted aliases; the
   JSON-import path instead writes the string `"auto"`, which is not an
   integer and will not validate if it reaches `spec.vlans`.
7. Plain shapes, text, and edges without semantic `schemaVars` are
   diagram-only and do not affect the topology.
8. Builder variables such as `$DEFAULT_MEMORY` are string-replaced before the
   generated topology is submitted.

Treat the diagram and generated topology as a pair. The topology is the
deployable source of truth for phēnix/minimega; XML is the editable visual
source. After changing XML programmatically, derive or update the topology
payload too — uploading XML alone does not regenerate `spec.nodes`.

### Node icons

Icon selection reads `general.vm_type` (`kvm` → `/virtual_machines`, otherwise
`/containers`) and `schemaVars.device` (`router`, `switch`, `desktop`, …).
These pick artwork only. `node.type` is the phēnix role —
`VirtualMachine`, `Router`, `Firewall`, and so on — and must never be
overwritten with `kvm` or `container`.

## Building Diagrams from Prompts, Images, or Inventory

Use this workflow when translating a user prompt, network-map image,
spreadsheet, CMDB export, or inventory file:

1. Extract an intermediate inventory: hostname, role/icon, VM type, OS image,
   CPU/memory, interfaces, IP/prefix, gateway, VLAN name/ID, and links.
2. Separate facts from assumptions. Do not invent IPs, images, VLAN IDs, or
   gateways when the input does not define them; use Builder defaults or mark
   unresolved values for user confirmation.
3. Normalize names to phēnix constraints: unique hostnames, valid config name,
   one canonical VLAN name per broadcast domain, and exact interface-to-VLAN
   membership.
4. Build and validate the phēnix topology object first. This catches semantic
   problems more reliably than reasoning directly in XML.
5. Create the mxGraph model from that object. Prefer cloning a small
   Builder-exported node/switch/edge as a template so styles and `schemaVars`
   escaping remain compatible. Assign unique cell IDs and valid edge
   `source`/`target` references.
6. Lay out nodes by trust zone, site, subnet, or the source image's spatial
   grouping. Use switch vertices for shared VLANs and direct semantic edges
   only where they describe the intended broadcast domain.
7. Keep decorative boundaries, titles, legends, and unimplemented devices free
   of semantic `schemaVars` so they cannot become accidental VMs.
8. Load the topology through `phenix config create`, fetch it through
   `GET /api/v1/builder/topologies/{name}`, and open **View JSON** to compare
   the generated nodes and VLAN aliases against the intermediate inventory.
9. Only create/start an experiment after the generated topology validates and
   every expected node, interface, and VLAN is accounted for.

For image input, visual links can cross, terminate at zone boxes, or omit
interface details. Trace endpoints carefully and use labels/legends to
disambiguate; do not infer that geometric proximity means connectivity. For
inventory input, join interface records to nodes by stable identifiers before
using display names, and detect duplicate hostnames/IPs and conflicting VLAN
IDs before producing XML.

## Builder Gotchas

- **Do not put topology semantics only in labels.** Translation reads
  `schemaVars`; a labeled icon without it is decorative.
- **Do not change `node.type` to `kvm` or `container`.** `type` is the phēnix
  role (`VirtualMachine`, `Router`, `Firewall`, …); VM implementation belongs
  in `general.vm_type`.
- **Save through the Builder endpoints.** `PUT /api/v1/configs/topology/{name}`
  replaces the whole config and drops annotations the caller omitted.
- **Preserve XML escaping.** JSON inside an XML attribute must escape quotes
  (normally `&quot;`). Prefer an XML library rather than string concatenation.
- **Validate both representations.** A valid mxGraph document can still
  generate an invalid topology, and valid topology YAML does not guarantee its
  diagram matches.
- **Write scenario topology membership exactly, but do not rely on exact
  reads.** The scenario `topology` annotation is a comma-separated list, and
  phēnix writes entries exactly and deduplicates them. Its own membership
  checks, however, use `strings.Contains` on the joined string
  (`types/scenario.go`, `api/experiment/experiment.go`), so a topology whose
  name is a substring of another entry still matches. Avoid names that nest
  inside one another.
- **Builder downloads are XML or SVG only.** There is no server-side raster or
  PDF rendering.
