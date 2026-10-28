// Package builder implements the versioned Builder document model. The model
// does not depend on a canvas library. [Document.ToTopology] publishes and
// [FromConfig] imports, both deterministically. Legacy XML and custom icons are
// untrusted input (see [DecodeLegacy], [ValidateIconPNG]). The API layer
// enforces size limits. This package enforces structure and semantics.
package builder
