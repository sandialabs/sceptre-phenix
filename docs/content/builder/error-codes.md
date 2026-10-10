# Error Codes

Every problem Builder reports has a code: a stable, machine-readable name
of the rule it breaks, such as `node.hostname.duplicate`. The diagram
checks in the editor and the Builder REST API use the same codes for the
same rules, so a script can act on a code without reading the message,
whose words may change.

A code is lowercase words joined by dots. The first word names what the
rule is about: `document`, `metadata`, `node`, `device`, `interface`,
`switch`, `network`, `edge` (a connection), `group`, `drawing`,
`scenario`, `source`, `include`, `template`, `icon`, `package`, `publish`,
`share`, `draft`, `import`, `request` or `server`. A code keeps its meaning
once it is released; a new rule gets a new code.

## Issues

Builder reports each problem as an issue:

| Field | Value |
|---|---|
| `code` | The code of the rule the issue breaks |
| `severity` | `error` when the issue refuses what was asked, `warning` when it does not |
| `message` | What the issue says, in words a person is shown |
| `path` | Where the issue is in the document, such as `nodes[3].device.hostname`, when it is about a part of it |
| `nodeId`, `edgeId`, `networkId` | The ID of the node, connection or network the issue is about, when it is about one |
| `field` | The device field the issue is about, such as `spec.network.interfaces.0.vlan`, when it is about one |

The severity in the table below is the one an issue of that code has by
default. The editor shows what only publishing refuses, such as an
interface without a VLAN, as a warning on the canvas, since a draft may
keep it, and as an error in the Publish dialog.

A key that an object of the document does not have
(`document.field.unknown`) and a value of the wrong type where the editor
checks the type too, such as a `createdBy` that is a number
(`metadata.user.not-text`), are issues as well, each at its path. Any
other value of the wrong type, such as a node position that is text, is
refused in the decoder's own words, without an issue.

## Errors of the REST API

When a Builder route refuses a request, the JSON body of the answer
carries the code of the failure in `code`, beside `message` and `cause`.
A document that does not validate (`document.invalid`), and a draft that
only publishing refuses (`publish.blocked`), also list each problem in
`issues`:

```json
{
  "code": "publish.blocked",
  "message": "topology lab cannot be published: interface \"eth1\" of device \"web\" has no VLAN: connect it to a network, or type a VLAN for it",
  "cause": "validating topology projection: interface \"eth1\" of device \"web\" has no VLAN: connect it to a network, or type a VLAN for it",
  "issues": [
    {
      "code": "interface.vlan.missing",
      "severity": "error",
      "message": "interface \"eth1\" of device \"web\" has no VLAN: connect it to a network, or type a VLAN for it",
      "path": "nodes[2].device.spec.network.interfaces[1]",
      "nodeId": "4113dfc0-f4be-57f9-8115-6cecd1f1aea3",
      "field": "spec.network.interfaces.1.vlan"
    }
  ]
}
```

A refusal about one of the scenarios a draft lists (`publish.scenario.*`)
names that scenario in `metadata.scenario`. A refused share list
(`share.users.refused`) lists each refused user and why in `errors`. The
answers about a draft that a client tells apart have codes of their own:
an If-Match that names an older version of the draft (`draft.stale`) or of
its share list (`draft.shares.stale`), a draft that changed while the
request was handled (`draft.conflict`), and the current version deleted
(`draft.snapshot.current`). A new topology name that a copy or a combined
import refuses has `import.name.invalid` or `import.name.source`. A method
that a Builder route does not take is answered with 405,
`request.method-not-allowed`, and the methods it takes in the `Allow`
header. A refusal that no rule of its
own names has the code of what its status says: `request.invalid` (400),
`request.forbidden` (403), `request.not-found` (404), `request.conflict`
(409), `request.stale` (412), `request.too-large` (413),
`request.unprocessable` (422), `server.error` (500), `server.busy` (503)
or `server.storage-full` (507).

Sharing and publishing templates and collections answers 200 with the
items it left as they were in `failed`, each with its `reason` and the
code of that reason: `template.item.not-found` or
`template.shares.too-many`.

A publication that goes through lists in `warnings`, and a partial one in
`errors`, an issue for each warning and failure. The Topology YAML download
(`POST /builder/export/topology`) lists its `warnings` and its
`publishBlockers` as issues too: one for each check that only publishing
makes, with the code of its first problem.

The Builder package routes refuse a package that does not decode or
validate with 422 and `package.invalid`, which lists in `issues` each
problem of a package that does not validate: a `package.*` code, or, for
its document, the code of the document's rule at a path below `document`.
A package larger than 5 MiB is refused with 413 and `package.too-large`.
The `warnings` of `POST /builder/package` are issues too: a config the
package names but does not carry (`package.config.unreadable`,
`package.include.file-path`), an icon it does not carry
(`package.icon.missing`, `package.icons.too-many`), and an entry it leaves
out of its requirements (`package.requirement.left-out`,
`package.requirements.truncated`). The dependencies of
`POST /builder/package/resolve` are data, not issues, and have no codes.

The warnings of an import or of a legacy diagram's conversion
(`POST /builder/generate`, `POST /builder/legacy`, and a document's
`source.warnings`) are plain text, without codes.

## Codes

<!-- codes:start -->

| Code | Severity | Rule |
|---|---|---|
| `document.invalid` | error | The document does not validate; issues lists each reason. |
| `document.content.missing` | error | There is no document at all. |
| `document.schema.mismatch` | error | The document's $schema is not the Builder document schema. |
| `document.revision.mismatch` | error | The document's revision is not the one this phenix reads. |
| `document.list.missing` | error | nodes, networks or edges is missing, null or not a list. |
| `document.viewport.not-finite` | error | A viewport value is not a finite number. |
| `document.zoom.not-positive` | error | The viewport zoom is not a positive number. |
| `document.grid.invalid` | error | The grid size is not a positive finite number. |
| `document.icon-size.unknown` | error | The document's icon size is not small, medium or large. |
| `document.layout.not-text` | error | The document's layout is not text. |
| `document.field.unknown` | error | An object of the document holds a key that object does not have. |
| `metadata.not-object` | error | The document's metadata is not an object. |
| `metadata.id.required` | error | The document has no ID. |
| `metadata.id.invalid` | error | The document ID is not a UUID. |
| `metadata.name.too-long` | error | The document name is longer than 512 bytes. |
| `metadata.name.control` | error | The document name holds a control character. |
| `metadata.user.not-text` | error | createdBy or updatedBy is not text. |
| `metadata.user.too-long` | error | createdBy or updatedBy is longer than 256 bytes. |
| `metadata.user.control` | error | createdBy or updatedBy holds a control character. |
| `metadata.time.not-text` | error | createdAt or updatedAt is not text. |
| `metadata.time.invalid` | error | createdAt or updatedAt is not a UTC time of the form YYYY-MM-DDTHH:MM:SSZ. |
| `metadata.notes.not-list` | error | The diagram's notes are not a list of text. |
| `metadata.notes.too-many` | error | The diagram has more than 100 notes. |
| `metadata.note.not-text` | error | A note of the diagram is not text. |
| `metadata.note.blank` | error | A note of the diagram is blank. |
| `metadata.note.too-long` | error | A note of the diagram is longer than 4096 bytes. |
| `metadata.note.control` | error | A note of the diagram holds a control character other than newline and tab. |
| `network.id.required` | error | A network has no ID. |
| `network.id.invalid` | error | A network ID is not a UUID. |
| `network.id.duplicate` | error | Two networks have the same ID, ignoring case. |
| `network.name.required` | error | A network has no name. |
| `network.name.whitespace` | error | A network name holds white space. |
| `network.name.duplicate` | error | Two networks have exactly the same name. |
| `network.line-style.unknown` | error | A network's line style is not one the editor draws. |
| `network.alias.out-of-range` | error | A network's VLAN alias is not from 1 to 4094. |
| `network.alias.duplicate` | error | Two networks have the same VLAN alias. |
| `network.switch.missing` | warning | A network has no switch on the canvas. |
| `node.id.required` | error | A node has no ID. |
| `node.id.invalid` | error | A node ID is not a UUID. |
| `node.id.duplicate` | error | Two nodes have the same ID, ignoring case. |
| `node.position.not-finite` | error | A node's position is not finite numbers. |
| `node.size.invalid` | error | A node's size is not positive finite numbers. |
| `node.kind.unknown` | error | A node's kind is not one the Builder knows. |
| `node.payload.missing` | error | A node lacks the payload of its kind. |
| `node.payload.extra` | error | A node carries the payload of another kind. |
| `node.hostname.required` | error | A device has no hostname. |
| `node.hostname.whitespace` | error | A device hostname holds white space. |
| `node.hostname.duplicate` | error | Two devices have the same hostname, ignoring case. |
| `node.hostname.short` | error | A device hostname is 1 character long, which phenix refuses. |
| `node.hostname.reserved` | error | A device hostname is "all", which minimega takes for every VM. |
| `node.hostname.numeric` | error | A device hostname is all digits, which phenix refuses in an experiment. |
| `node.hostname.windows-phenix` | error | A Windows device is named "phenix", which phenix refuses in an experiment. |
| `node.hostname.reserved-case` | warning | A device hostname differs from "all" only by case. |
| `node.hostname.phenix` | warning | A device that is not Windows is named "phenix", the hostname of phenix's own images. |
| `node.parent.self` | error | A node is its own parent. |
| `node.parent.unknown` | error | A node's parent is no node of the document. |
| `node.parent.not-group` | error | A node's parent is not a group. |
| `node.parent.cycle` | error | Groups contain each other in a cycle. |
| `device.spec.required` | error | A device has no spec. |
| `device.hostname.mismatch` | warning | A device's spec names another hostname than the device; the device's wins. |
| `device.icon-key.unknown` | error | A device's icon key is not a built-in icon. |
| `device.icon.invalid` | error | A device's custom icon is not an icon name. |
| `device.icon-size.unknown` | error | A device's icon size is not small, medium or large. |
| `device.color.invalid` | error | A device's outline or fill color is not #rrggbb. |
| `device.included-from.invalid` | error | The topology a device is included from is blank or holds white space. |
| `device.included-from.no-includes` | error | A device is included from a topology, but the document includes none. |
| `device.interfaces.none` | warning | A device has no interfaces. |
| `device.image.unknown` | warning | A device's drive image is not one of the server's disk images. |
| `interface.id.required` | error | An interface connection point has no ID. |
| `interface.id.invalid` | error | An interface connection point ID is not a UUID. |
| `interface.id.duplicate` | error | Two interface connection points have the same ID, ignoring case. |
| `interface.name.required` | error | An interface connection point has no name. |
| `interface.name.duplicate` | error | Two connection points of a device have the same name, ignoring case. |
| `interface.name.unmatched` | warning | A connection point names no interface of the device's spec, so publishing drops it. |
| `interface.vlan.missing` | error | An interface of a device phenix starts has no VLAN, which blocks publishing. |
| `interface.vlan.implicit` | warning | An unconnected interface's VLAN puts it on a network of the diagram when published. |
| `interface.vlan.case-mismatch` | warning | An interface's VLAN differs from a network's name only in case, which phenix keeps apart. |
| `interface.vlan.unknown` | warning | An interface's VLAN names no network of the diagram. |
| `interface.network.unconnected` | warning | An interface is not connected to a network. |
| `interface.ip.shared` | error | Interfaces on one network use the same IP address, which blocks publishing. |
| `interface.mac.shared` | error | Interfaces on one network use the same MAC address, which blocks publishing. |
| `switch.network.required` | error | A switch names no network. |
| `switch.network.unknown` | error | A switch names a network the document does not have. |
| `switch.color.invalid` | error | A switch's outline or fill color is not #rrggbb. |
| `switch.icon-size.unknown` | error | A switch's icon size is not small, medium or large. |
| `switch.notes.not-list` | error | A switch's notes are not a list of text. |
| `switch.notes.too-many` | error | A switch has more than 100 notes. |
| `switch.note.not-text` | error | A note of a switch is not text. |
| `switch.note.blank` | error | A note of a switch is blank. |
| `switch.note.too-long` | error | A note of a switch is longer than 4096 bytes. |
| `switch.note.control` | error | A note of a switch holds a control character other than newline and tab. |
| `group.border-style.unknown` | error | A group's border style is not one the editor draws. |
| `group.icon-key.unknown` | error | A group's icon key is not a built-in icon. |
| `group.icon.invalid` | error | A group's custom icon is not an icon name. |
| `group.icon-size.unknown` | error | A group's icon size is not small, medium or large. |
| `drawing.shape.unknown` | error | A shape's figure is not rectangle or circle. |
| `drawing.color.invalid` | error | A shape's or a line's color is not #rrggbb. |
| `drawing.border-style.unknown` | error | A shape's border style is not one the editor draws. |
| `drawing.icon.ambiguous` | error | An icon drawing names both or neither of a built-in icon and a custom icon. |
| `drawing.icon-key.unknown` | error | An icon drawing's icon key is not a built-in icon. |
| `drawing.icon.invalid` | error | An icon drawing's custom icon is not an icon name. |
| `drawing.points.too-few` | error | A line has fewer than 2 points. |
| `drawing.points.too-many` | error | A line has more than 64 points. |
| `drawing.points.not-finite` | error | A point of a line is not finite numbers. |
| `drawing.line-style.unknown` | error | A line's style is not one the editor draws. |
| `drawing.arrow.not-boolean` | error | A line's startArrow or endArrow is not true or false. |
| `edge.id.required` | error | A connection has no ID. |
| `edge.id.invalid` | error | A connection ID is not a UUID. |
| `edge.id.duplicate` | error | Two connections have the same ID, ignoring case. |
| `edge.route.too-few` | error | A connection's route has fewer than 2 points. |
| `edge.route.not-finite` | error | A point of a connection's route is not finite numbers. |
| `edge.line-style.unknown` | error | A connection's line style is not one the editor draws. |
| `edge.source.unknown` | error | A connection's source is no node of the document. |
| `edge.target.unknown` | error | A connection's target is no node of the document. |
| `edge.endpoints.same` | error | A connection joins a node to itself. |
| `edge.endpoints.invalid` | error | A connection does not join one device interface to one switch. |
| `edge.handle.unknown` | error | A connection names a connection point its device does not have. |
| `edge.interface.connected` | error | An interface is connected by more than one connection. |
| `edge.network.required` | error | A connection names no network. |
| `edge.network.unknown` | error | A connection names a network the document does not have. |
| `edge.network.mismatch` | error | A connection names another network than its switch. |
| `scenario.list.too-many` | error | The document lists more than 20 scenarios. |
| `scenario.name.required` | error | A listed scenario has no name. |
| `scenario.name.too-long` | error | A scenario name is longer than 256 bytes. |
| `scenario.name.invalid` | error | A scenario name is not a config name. |
| `scenario.name.duplicate` | error | A scenario is listed twice, ignoring case. |
| `source.kind.unknown` | error | The source's kind is not manual, topology or experiment. |
| `source.digest.malformed` | error | The source digest is not sha256: and 64 hex digits. |
| `source.annotations.not-object` | error | The source annotations are not an object of text values. |
| `source.annotations.too-many` | error | The source carries more than 100 annotations. |
| `source.annotations.too-large` | error | The source annotations take more than 256 KiB. |
| `source.annotation.not-text` | error | A source annotation's value is not text. |
| `source.annotation-key.blank` | error | A source annotation key is blank. |
| `source.annotation-key.too-long` | error | A source annotation key is longer than 512 bytes. |
| `source.annotation-key.control` | error | A source annotation key holds a control character. |
| `include.list.not-list` | error | The included topologies are not a list of names. |
| `include.name.required` | error | An included topology has no name. |
| `include.name.whitespace` | error | The name of an included topology holds white space. |
| `template.list.not-list` | error | The document's templates are not a list. |
| `template.list.too-many` | error | The document carries more than 50 templates. |
| `template.id.required` | error | A template has no ID. |
| `template.id.invalid` | error | A template ID is not a UUID. |
| `template.id.duplicate` | error | Two templates have the same ID, ignoring case. |
| `template.name.required` | error | A template has no name. |
| `template.name.too-long` | error | A template name is longer than 128 bytes. |
| `template.name.control` | error | A template name holds a control character. |
| `template.name.duplicate` | error | Two templates of a template file have the same name, ignoring case. |
| `template.description.too-long` | error | A template description is longer than 1024 bytes. |
| `template.description.control` | error | A template description holds a control character. |
| `template.spec.required` | error | A template's device has no spec. |
| `template.hostname.required` | error | A template's spec has no general.hostname. |
| `template.hostname.whitespace` | error | A template's hostname holds white space. |
| `template.icon-key.unknown` | error | A template's icon key is not a built-in icon. |
| `template.icon.invalid` | error | A template's custom icon is not an icon name. |
| `template.icon-size.unknown` | error | A template's icon size is not small, medium or large. |
| `template.color.invalid` | error | A template's outline or fill color is not #rrggbb. |
| `template.device.unencodable` | error | A template's device cannot be encoded as JSON. |
| `template.device.too-large` | error | A template's device takes more than 16 KiB as JSON. |
| `template.file.invalid` | error | A template file does not validate; issues lists each reason. |
| `template.file.schema-mismatch` | error | A template file's $schema is not the template file schema. |
| `template.file.empty` | error | A template file holds no template. |
| `template.file.too-many` | error | A template file holds more than 200 templates. |
| `template.collection-name.required` | error | A template file's collection has no name. |
| `template.collection-name.too-long` | error | A collection name is longer than 128 bytes. |
| `template.collection-name.control` | error | A collection name holds a control character. |
| `template.collection-description.too-long` | error | A collection description is longer than 1024 bytes. |
| `template.collection-description.control` | error | A collection description holds a control character. |
| `template.item.not-found` | error | A template or collection a share or publish request names is not in the library. |
| `template.shares.too-many` | error | A template or collection would be shared with more users than it may be. |
| `icon.list.not-object` | error | The custom icons are not an object of icons by name. |
| `icon.list.too-many` | error | More than 50 custom icons are carried. |
| `icon.name.invalid` | error | A custom icon's name is not an icon name. |
| `icon.data.invalid` | error | A custom icon's data is not base64 of at most 40960 bytes. |
| `icon.png.invalid` | error | A custom icon is not a PNG the Builder accepts. |
| `package.invalid` | error | A Builder package does not decode or validate; issues lists what does not validate. |
| `package.too-large` | error | A Builder package is larger than 5 MiB. |
| `package.schema.mismatch` | error | A package's $schema is not the Builder package schema. |
| `package.document.missing` | error | A package holds no Builder document. |
| `package.document.invalid` | error | A package's document does not decode as a Builder document. |
| `package.configs.too-many` | error | A package carries more than 20 Scenario configs or 100 Topology configs. |
| `package.config-key.invalid` | error | The key a package keeps a config under is not a config name. |
| `package.config-kind.mismatch` | error | A carried config's kind is not the kind of its list. |
| `package.config-name.mismatch` | error | A carried config's metadata.name is not the key the package keeps it under. |
| `package.config-api-version.invalid` | error | A carried config's apiVersion is not phenix.sandia.gov/v and a number. |
| `package.config-spec.missing` | error | A carried config has no spec. |
| `package.config.unlisted` | error | A package carries a config its requirements do not name. |
| `package.config-annotation.builder` | error | A carried config has a Builder annotation, which a package never carries. |
| `package.requirements.missing` | error | A list of a package's requirements is missing or null. |
| `package.requirements.too-many` | error | A list of a package's requirements holds more than 1000 entries. |
| `package.requirement.blank` | error | An entry of a package's requirements is blank. |
| `package.requirement.too-long` | error | An entry of a package's requirements is longer than 4096 bytes. |
| `package.requirement.control` | error | An entry of a package's requirements holds a control character. |
| `package.config.unreadable` | warning | A Scenario config or included topology the document names does not exist, or the caller may not read it, so the package does not carry it. |
| `package.include.file-path` | warning | An included topology is a file path, which a package never carries. |
| `package.icon.missing` | warning | A custom icon the document names is not in the icon library, so the package does not carry it. |
| `package.icons.too-many` | warning | The document names more custom icons than the 50 a package carries; the package does not carry the rest. |
| `package.requirement.left-out` | warning | A name or path the document or a config holds is blank, longer than 4096 bytes or holds a control character, so the package does not list it. |
| `package.requirements.truncated` | warning | A list of the requirements would hold more than 1000 entries, so the package lists the first 1000. |
| `publish.blocked` | error | Only publishing refuses the topology; issues lists each interface, address or hostname to fix. |
| `publish.topology.invalid` | error | phenix's topology schema refuses the topology the document publishes as. |
| `publish.topology.exists` | error | The topology a publish creates already exists. |
| `publish.topology.missing` | error | The topology a publish updates does not exist. |
| `publish.topology.not-source` | error | The topology is not the one this draft was loaded from, so it cannot update it. |
| `publish.topology.changed` | error | The topology changed after this draft published it. |
| `publish.topology.file-mismatch` | error | The topology is not what its Builder file publishes, so it cannot be updated. |
| `publish.topology.file-changed` | error | The topology or its Builder file changed after this draft was opened from the file. |
| `publish.experiment.exists` | error | The experiment a publish creates already exists. |
| `publish.experiment.missing` | error | The experiment a publish updates does not exist. |
| `publish.experiment.not-source` | error | The experiment is not the one this draft was loaded from, so it cannot update it. |
| `publish.experiment.changed` | error | The experiment changed after this draft published it. |
| `publish.experiment.running` | error | A running experiment cannot be updated. |
| `publish.experiment.invalid` | error | phenix refuses the experiment a publish would write. |
| `publish.experiment.reserved` | error | The experiment is named "all", which phenix reserves. |
| `publish.experiment.name-too-long` | error | The experiment name is longer than a bridge name this server names after it. |
| `publish.experiment.unmergeable` | error | An included topology cannot be merged into the experiment an update writes. |
| `publish.source.changed` | error | The config this draft was imported from changed since. |
| `publish.scenario.missing` | error | A scenario the document lists does not exist, or the caller may not read it. |
| `publish.scenario.not-listed` | error | The experiment's scenario is not one the document lists. |
| `publish.scenario.invalid` | error | A listed scenario would not be valid with the topology added. |
| `publish.include.clash` | error | A hostname is defined both in the topology and in a topology it includes. |
| `publish.include.hostname` | error | An included topology has a hostname phenix refuses in an experiment. |
| `publish.target.invalid` | error | A topology, experiment or scenario name is not a config name. |
| `publish.stage.failed` | error | A publish stage failed after earlier stages were written. |
| `publish.cleanup.failed` | warning | The publication went through, but content it replaces could not be removed. |
| `publish.broadcast.failed` | warning | A config was stored, but its live update could not be broadcast. |
| `publish.file.unchanged` | warning | The topology names a Builder file that publishing does not change. |
| `publish.legacy.replaced` | warning | The topology's legacy Builder diagram was replaced by this diagram. |
| `publish.legacy.removed` | warning | The topology's legacy Builder diagram could not be read and was removed. |
| `publish.experiment.unrecorded` | warning | The experiment was stored, but not which draft published it. |
| `publish.scenario.partial` | warning | Some scenarios were updated before the scenario stage failed. |
| `publish.retry.complete` | warning | The same publication was already complete; nothing was written. |
| `preflight.capacity.cpu` | warning | The schedulable hosts have fewer free CPUs than the devices take; VMs would share CPUs. |
| `preflight.capacity.memory` | error | The schedulable hosts have less free memory than the diagram's devices take. |
| `preflight.capacity.vm-too-large` | error | A device fits on no single schedulable host: none has both its CPUs and its memory. |
| `preflight.network.vlan-range` | error | The experiment's VLAN range is too small for the diagram, or leaves out a VLAN alias. |
| `preflight.network.alias-in-use` | error | A network's VLAN alias is a VLAN a running experiment already uses. |
| `preflight.network.bridge-missing` | warning | A bridge the diagram's interfaces or the experiment use does not exist on a host yet. |
| `preflight.disk.missing` | error | A drive image is not one of the server's disk images. |
| `preflight.disk.kind` | error | A drive image is not of the kind its device needs: VM or ISO for kvm, container otherwise. |
| `preflight.app.missing` | error | A scenario names an app that is not a default, built-in or user app of this server. |
| `preflight.app.scenario-missing` | error | A scenario the diagram lists does not exist. |
| `preflight.app.scenario-unreadable` | warning | The caller may not read a scenario the diagram lists, or it cannot be read now. |
| `preflight.unavailable` | warning | A preflight check, or part of one, could not be made; the message says why. |
| `share.users.refused` | error | Some users of a share list were refused; errors names each and why. |
| `draft.stale` | error | The If-Match entity tag names a version of the draft that is no longer current. |
| `draft.conflict` | error | The draft changed while the request was handled; read it again and retry. |
| `draft.snapshot.current` | error | The current version of a draft cannot be deleted. |
| `draft.shares.stale` | error | The If-Match entity tag names a share list of the draft that is no longer current. |
| `import.name.invalid` | error | The new topology name of a copy or a combined import is not a config name. |
| `import.name.source` | error | The new topology name of a copy or a combined import is the imported topology's own name. |
| `request.invalid` | error | The request is malformed: its body, a header or a parameter. |
| `request.forbidden` | error | The caller's role does not allow the operation. |
| `request.not-found` | error | What the request names does not exist, or the caller may not see it. |
| `request.method-not-allowed` | error | The route does not take the request's method; Allow names the methods it takes. |
| `request.conflict` | error | The request conflicts with what is stored. |
| `request.stale` | error | The If-Match entity tag names a version that is no longer current. |
| `request.too-large` | error | The request or what it stores is larger than a limit. |
| `request.unprocessable` | error | The request is well formed, but what it asks for cannot be done. |
| `server.error` | error | The server failed; its log says why. |
| `server.busy` | error | The server gave up because what was asked kept changing; try again. |
| `server.storage-full` | error | The store refused a write for lack of space. |

<!-- codes:end -->
