# Builder downloads and packages

Part of the [Builder references](../builder.md). The Download dialog's
Topology YAML and Gephi (GEXF) files, and Builder packages: their format,
the routes that build and resolve them, and their Download and Upload UI.

## Topology YAML

The Download dialog's Topology YAML saves `<diagram name>.topology.yaml`, the
Topology config Publish would write, from `POST /builder/export/topology`
(`configs` `get`; nothing is written). The request carries the document,
edits not yet saved included, and the topology `name` the Publish dialog
proposes. The config has no annotations and names included topologies in
`includeTopologies` rather than merging them. What only Publish refuses,
interfaces with a blank VLAN, shared addresses and the hostnames phenix
refuses when it creates an experiment, is not refused but named in
`publishBlockers`, which the dialog shows after the download: one entry per
check, interfaces without a VLAN first, then shared addresses, then
hostnames, each as Publish's 422 `message` words it. A document with several
gets each, where Publish's 422 names only the first. A document phenix's
schema refuses is refused with 422, as Publish refuses it. Publish names
interfaces without a VLAN, then shared addresses, then hostnames, before any
other reason; this route names interfaces without a VLAN only when a `vlan`
is missing or null, and hostnames only when one is a single character, which
the schema refuses too, and otherwise gives the schema's reason.

## Gephi (GEXF)

The Download dialog's Gephi (GEXF) saves `<diagram name>.gexf`, a GEXF 1.3
graph for Gephi that Builder cannot open, made in the browser. Devices
and networks are its nodes, each connection an edge from a device to its
network, and their settings are columns; notes, groups and drawings are not
nodes. It lists each device's scenario apps (`apps`, `disabled_apps`) of
every listed scenario, reading each as the Inspector does; when it cannot
read one, it leaves the apps out and
the dialog says why. The file names gexf.xsd in `xsi:schemaLocation`, as
Gephi writes it. The official RelaxNG grammar, gexf.rng, rejects that
attribute (gexf.net says to remove it before using xmllint), so to check a
file as saved, use a grammar that includes gexf.rng unchanged and allows the
attribute on `<gexf>`, the only element that uses `gexf-content`:

```xml
<grammar xmlns="http://relaxng.org/ns/structure/1.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         datatypeLibrary="http://www.w3.org/2001/XMLSchema-datatypes">
  <include href="gexf.rng"/>
  <define name="gexf-content" combine="interleave">
    <optional>
      <attribute name="xsi:schemaLocation">
        <list><oneOrMore><data type="anyURI"/><data type="anyURI"/></oneOrMore></list>
      </attribute>
    </optional>
  </define>
</grammar>
```

## Builder packages

A package (`bdoc.Package`, `types/builder/package.go`; `package.js`) is one
JSON or YAML file: `{$schema:
"https://phenix.sandia.gov/schemas/builder/package/v1", document,
scenarios?: {<name>: config}, topologies?: {<name>: config},
requirements: {scenarios, topologies, templates, icons, images: [{name,
usedBy}], apps, files}}`. A config is `{apiVersion, kind, metadata: {name,
annotations?}, spec}`, of the kind of its list, named by its key, and named
by the requirements; it is data only (no times, no `builder-*`
annotations: `Validate` refuses each `IsBuilderAnnotation` key, one issue
per key, `<list>.<name>.metadata.annotations: "builder-doc" is a Builder
annotation, which a package never carries`, so no config created from a
package claims a document). Every requirements list is present (empty
allowed), at most 1000 entries of at most 4096 bytes, none blank or with
control characters. Strict everywhere (unknown fields refused), YAML
through `JSONFromYAML`, at most 5 MiB (`MaxPackageBytes`), at most 20
scenarios (`MaxScenarios`) and 100 topologies (`MaxPackageTopologies`).
`decodePackage` in `package.js` makes the same checks with the same words
(it stops at the first). `ParsePackage`, `DecodePackage`,
`Package.Validate`/`Issues` (`*PackageError`, `ErrInvalidPackage`; each
issue has a `package.*` code, e.g. `package.config-spec.missing`,
`package.requirements.missing`, or, for the document, the document rule's
code at a path below `document`; `DecodePackage` gives a document `Decode`
refuses the same way, and `ErrorIssues` reads a `*PackageError`),
`NewPackage(document, PackageContents)` (lists entries as named, so it may
not validate), `Package.TrimRequirements` (leaves out each entry that is
blank, too long or has control characters, then entries past 1000, and
returns a warning issue naming each, at the list's path:
`package.requirement.left-out` `The package does not list file
"/a\nb": it must not contain control characters.`,
`package.requirements.truncated` `The package lists at most 1000 apps, so
it does not list 4 more: "a", "b", "c" and 1 more.`),
`Document.IconNames`; schema `PackageSchema()`
(`package_schema.go`, documented like the others, checked by
`TestPackageSchemaDocumentsEveryProperty`), served by `GET
/schemas/builder/package/v1` (`schemas` `get` on `builder`). The docs
example `docs/content/builder/examples/pump-station.package.yaml` must load
(`TestDocsPackageExampleLoads`). `IsDocumentText` is false for a package.
A package never holds a file's content, a script or an app.

### Building a package

`POST /builder/package` (`web/builder_package.go`, `configs` `get`) takes
`{document, include: [scenarios|topologies|icons|images]}` (strict; an
unknown or repeated section is 400) and answers `{package, warnings}`;
each warning is a `BuilderIssue` without a path (`package.config.unreadable`,
`package.include.file-path`, `package.icon.missing`,
`package.icons.too-many`, and the trim's two codes), which the Download
dialog normalizes with `toIssue` and shows with its code.
Requirements, whatever is included: the document's `scenarios`, its
`source.includeTopologies`, its templates' names, `IconNames`, the apps of
the Scenario configs that could be read (`apps[].name`, v2, or v1's
`experiment` and `host` lists), and the files (device and carried topology
node `injections[].src`, carried scenario `apps[].assetDir`); `images` (each
drive `hardware.drives[].image` of the document's devices and carried
topology nodes, with sorted `usedBy` hostnames) only with `images`. Each
listed scenario is read with `configs` `get` and `scenarios` `list` (carried
with `scenarios`), each include with `topologies` (`configs` `get`,
`topologies` `list`; a file path is never read). Missing and forbidden read
alike and give a warning `Scenario config <n> does not exist on this
server, or your role cannot read it: the package names it but does not
carry it.` (`... the package lists none of its apps.` without `scenarios`;
`Included topology <n> ...`; `Included topology <path> is a file path: ...`).
`icons` copies each named icon the document lacks from the library (at most
50; a missing one warns). Then `TrimRequirements` (its warnings join the
others; e.g. an injection `src` with a newline, or an app name a stored
scenario holds with a control character) and `Validate`: what it still
refuses (a stored config without a spec) is 422 `package.invalid` with the
issues in `cause` and `issues`, so the route never answers a package the
resolve route refuses. Over 5 MiB is 413 `package.too-large`. Nothing is
written.

### Resolving a package

`POST /builder/package/resolve` (`configs` `get`; the body is the package,
at most `MaxPackageBytes` through `builderDecodeLimit`, else 413
`package.too-large` before decoding; strict, 422 `package.invalid` when it
does not decode or validate, with `issues` when it does not validate)
answers `{dependencies:
[{kind, name, status, packaged, detail?}]}` in requirement order by kind
(scenario, topology, template, icon, image, app, file). Configs: `present`
(same spec as the package's copy by canonical JSON, or no copy), `different`,
`missing`, or `unknown` when the caller may not read it (`configs` `get` and
the kind's `list`) or the name is no config name. Icons: the library by
name (`configs` `list`), bytes compared. Images: `disks` `list`, then the
listing (`builderAPI.disks`, the cached `builderDiskLister` the publish dry
run and preflight share, default `disk.GetImages("")`) filtered by `disks`
`list` on each image's name as `GET /disks` filters it, so an image the
caller may not see is `missing` like an absent one; matched by name or full
path, else by file name (`builderFindDisk`), and then the detail says so
(`Matched by file name pkg.qc2; this server's image is
/phenix/images/pkg.qc2.`, `builderDiskMatch`); a listing error, or a
listing of no images at all (what `disk.GetImages` gives without an error
when minimega cannot be reached), is `unknown` with "The server's disk
images could not be listed.". Apps: `applications` `list`, then the listing (`appNames`,
default `app.List()` plus `app.DefaultApps()`) filtered by `applications`
`list` on each name (`listedAppNames`), so an app the caller may not see is
`missing` like an absent one; `unknown` only without `applications` `list`
at all. Templates are
`present`; files always `unknown`. Nothing is written; the tests inject
`withBuilderDisks` and `withBuilderApps`.

### Package UI

UI: Download dialog's **Builder package** fieldset (ticks
`download-package-<section>`, none ticked; `download-package-format` JSON or
YAML; button `download-package`; `store.buildPackage`); warnings hold the
file (`download-package-held`, `download-package-save`, `-discard`) until
the user saves; the file is `<name>.package.json|yaml` (`packageFileName`).
Upload recognizes a package by `$schema` (`isPackageValue`), decodes it
(`decodePackage`), calls `store.resolvePackage`, and shows
`dialogs/PackageImport.vue` (testids `upload-package`,
`upload-package-group-<kind>`, `upload-package-item-<kind>-<name>`,
`upload-package-status-<kind>-<name>`, `upload-package-create-<kind>-<name>`,
`upload-package-continue`, `upload-package-cancel`): status in words
(Present, Missing, Different, Not checked, ", in the package"), an unticked
"Create on this server" checkbox only for a missing carried scenario or
topology (`canCreate`). Continue creates the ticked configs one at a time
(`createTickedConfigs`, `store.createPackagedConfig`, `POST /configs`; a
failure is a warning and the rest go on), runs `ingestIcons`, then opens the
document as a new draft as a plain upload does (`openUploaded`); failures
and icon warnings show first in `ImportWarnings`, whose summary names the
configs created first (`Created Scenario config <n> on this server. This
upload has 1 warning.`, `createdConfigs`), and Cancel or closing that view
announces `Created … on this server.` again. While Continue works
(`creating` in `UploadDialog.vue`), `upload-package-cancel` is
aria-disabled and described by `upload-package-progress` (the status
text), and `requestClose` ignores Close, Escape and the backdrop, as
`ShareDialog` does while it saves. References are never rewritten. e2e:
`builder-package.spec.js` (with its `@axe` test; `page.route` stubs a
failing and a held `POST /configs` and a held `POST /builder/package`).
