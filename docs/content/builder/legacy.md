# Legacy Builder

Earlier phenix releases had another graphical editor, also named Builder. It
opened from the **Builder** tab in a new browser tab, at `/builder`, and kept
its diagram as mxGraph XML in the `builder-xml` annotation of the Topology
config.

The legacy Builder was removed in
[sandialabs/sceptre-phenix#442](https://github.com/sandialabs/sceptre-phenix/pull/442).
The last commit that has it is
[`a0aeaa4e`](https://github.com/sandialabs/sceptre-phenix/commit/a0aeaa4ee899018196ca4a4aa6e9ce114cf66de4).
The [Builder](index.md) replaces it.

These routes went with it: `GET /builder`, `POST /builder/save`,
`GET` and `POST /api/v1/builder/topologies`, `GET` and
`PUT /api/v1/builder/topologies/{name}`, and `POST` and
`PUT /api/v1/experiments/builder`.

## Topologies the legacy Builder saved

A topology that the legacy Builder saved keeps its `builder-xml` annotation.
On the **Configs** page it has the tag `builder legacy`, the viewer shows the
annotation as `<SNIPPED>`, and **Edit** does not open it as text.

The Builder imports such a topology as it imports any stored topology (see
[Import, Upload and Download](import-upload-download.md)), but it cannot
update it: publish your diagram under a new topology name.
