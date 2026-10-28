package builder

import (
	"errors"
	"slices"
)

// Severity is how much an [Issue] weighs: an error refuses what was asked
// (saving a document, publishing it, a request), a warning does not.
type Severity string

// The severities an [Issue] has.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Code is the stable, machine-readable name of the rule an [Issue], a publish
// blocker or a Builder REST error reports, such as node.hostname.duplicate. A
// published code never changes meaning. The registry in this file lists every
// one, [LookupCode] reads it, and the UI's codes.json and the docs' error code
// table are made from it.
type Code string

// CodeInfo describes a [Code]: the severity an issue with it has by
// default, and the rule it names, in one sentence.
type CodeInfo struct {
	Code        Code     `json:"code"`
	Severity    Severity `json:"severity"`
	Description string   `json:"description"`
}

// Codes of the document as a whole.
const (
	CodeDocumentInvalid           Code = "document.invalid"
	CodeDocumentContentMissing    Code = "document.content.missing"
	CodeDocumentSchemaMismatch    Code = "document.schema.mismatch"
	CodeDocumentRevisionMismatch  Code = "document.revision.mismatch"
	CodeDocumentListMissing       Code = "document.list.missing"
	CodeDocumentViewportNotFinite Code = "document.viewport.not-finite"
	CodeDocumentZoomNotPositive   Code = "document.zoom.not-positive"
	CodeDocumentGridInvalid       Code = "document.grid.invalid"
	CodeDocumentIconSizeUnknown   Code = "document.icon-size.unknown"
	CodeDocumentLayoutNotText     Code = "document.layout.not-text"
	CodeDocumentFieldUnknown      Code = "document.field.unknown"
)

// Codes of the document metadata.
const (
	CodeMetadataNotObject    Code = "metadata.not-object"
	CodeMetadataIDRequired   Code = "metadata.id.required"
	CodeMetadataIDInvalid    Code = "metadata.id.invalid"
	CodeMetadataNameTooLong  Code = "metadata.name.too-long"
	CodeMetadataNameControl  Code = "metadata.name.control"
	CodeMetadataUserNotText  Code = "metadata.user.not-text"
	CodeMetadataUserTooLong  Code = "metadata.user.too-long"
	CodeMetadataUserControl  Code = "metadata.user.control"
	CodeMetadataTimeNotText  Code = "metadata.time.not-text"
	CodeMetadataTimeInvalid  Code = "metadata.time.invalid"
	CodeMetadataNotesNotList Code = "metadata.notes.not-list"
	CodeMetadataNotesTooMany Code = "metadata.notes.too-many"
	CodeMetadataNoteNotText  Code = "metadata.note.not-text"
	CodeMetadataNoteBlank    Code = "metadata.note.blank"
	CodeMetadataNoteTooLong  Code = "metadata.note.too-long"
	CodeMetadataNoteControl  Code = "metadata.note.control"
)

// Codes of networks.
const (
	CodeNetworkIDRequired     Code = "network.id.required"
	CodeNetworkIDInvalid      Code = "network.id.invalid"
	CodeNetworkIDDuplicate    Code = "network.id.duplicate"
	CodeNetworkNameRequired   Code = "network.name.required"
	CodeNetworkNameWhitespace Code = "network.name.whitespace"
	CodeNetworkNameDuplicate  Code = "network.name.duplicate"
	CodeNetworkLineStyle      Code = "network.line-style.unknown"
	CodeNetworkAliasRange     Code = "network.alias.out-of-range"
	CodeNetworkAliasDuplicate Code = "network.alias.duplicate"
	CodeNetworkSwitchMissing  Code = "network.switch.missing"
	CodeNetworkSubnetInvalid  Code = "network.subnet.invalid"
	CodeNetworkGatewayInvalid Code = "network.gateway.invalid"
	CodeNetworkGatewayOutside Code = "network.gateway.outside-subnet"
	CodeNetworkGatewayUnowned Code = "network.gateway.unowned"
	CodeNetworkSubnetOverlap  Code = "network.subnet.overlap"
)

// Codes of nodes of every kind, and of the hostname of a device node.
const (
	CodeNodeIDRequired           Code = "node.id.required"
	CodeNodeIDInvalid            Code = "node.id.invalid"
	CodeNodeIDDuplicate          Code = "node.id.duplicate"
	CodeNodePositionNotFinite    Code = "node.position.not-finite"
	CodeNodeSizeInvalid          Code = "node.size.invalid"
	CodeNodeKindUnknown          Code = "node.kind.unknown"
	CodeNodePayloadMissing       Code = "node.payload.missing"
	CodeNodePayloadExtra         Code = "node.payload.extra"
	CodeNodeHostnameRequired     Code = "node.hostname.required"
	CodeNodeHostnameWhitespace   Code = "node.hostname.whitespace"
	CodeNodeHostnameDuplicate    Code = "node.hostname.duplicate"
	CodeNodeHostnameShort        Code = "node.hostname.short"
	CodeNodeHostnameReserved     Code = "node.hostname.reserved"
	CodeNodeHostnameNumeric      Code = "node.hostname.numeric"
	CodeNodeHostnameWindows      Code = "node.hostname.windows-phenix"
	CodeNodeHostnameReservedCase Code = "node.hostname.reserved-case"
	CodeNodeHostnamePhenix       Code = "node.hostname.phenix"
	CodeNodeParentSelf           Code = "node.parent.self"
	CodeNodeParentUnknown        Code = "node.parent.unknown"
	CodeNodeParentNotGroup       Code = "node.parent.not-group"
	CodeNodeParentCycle          Code = "node.parent.cycle"
)

// Codes of the payload of a device node.
const (
	CodeDeviceSpecRequired         Code = "device.spec.required"
	CodeDeviceHostnameMismatch     Code = "device.hostname.mismatch"
	CodeDeviceIconKeyUnknown       Code = "device.icon-key.unknown"
	CodeDeviceIconInvalid          Code = "device.icon.invalid"
	CodeDeviceIconSizeUnknown      Code = "device.icon-size.unknown"
	CodeDeviceColorInvalid         Code = "device.color.invalid"
	CodeDeviceLayerRange           Code = "device.layer.out-of-range"
	CodeDeviceHideDescriptionType  Code = "device.hide-description.not-boolean"
	CodeDeviceIncludedFromInvalid  Code = "device.included-from.invalid"
	CodeDeviceIncludedFromNoSource Code = "device.included-from.no-includes"
	CodeDeviceInterfacesNone       Code = "device.interfaces.none"
	CodeDeviceDrivesNone           Code = "device.drives.none"
	CodeDeviceImageUnknown         Code = "device.image.unknown"
	CodeDeviceRulesetsIgnored      Code = "device.rulesets.ignored"
	CodeDeviceRouterOSUnsupported  Code = "device.router-os.unsupported"
	CodeDeviceInterfacesUnset      Code = "device.interfaces.unconfigured"
	CodeDeviceSerialUnset          Code = "device.serial.unconfigured"
	CodeDeviceInjectionsUnapplied  Code = "device.injections.unapplied"
	CodeDeviceOSPFMissing          Code = "device.ospf.missing"
	CodeDeviceOSPFUnused           Code = "device.ospf.unused"
	CodeDeviceOSPFNetworkUnused    Code = "device.ospf-network.unused"
	CodeDeviceNTPServerInvalid     Code = "device.ntp-server.invalid"
	CodeDeviceNTPServerIgnored     Code = "device.ntp-server.ignored"
	CodeDeviceNTPServerNoMgmt      Code = "device.ntp-server.no-mgmt"
	CodeDeviceEnableSSHInvalid     Code = "device.enable-ssh.invalid"
	CodeDeviceAutotunnelInvalid    Code = "device.autotunnel.invalid"
	CodeDeviceDefaultAppsInvalid   Code = "device.default-apps.invalid"
)

// Codes of the interfaces of a device: its connection points (interface
// handles) and the interfaces of its spec.
const (
	CodeInterfaceIDRequired     Code = "interface.id.required"
	CodeInterfaceIDInvalid      Code = "interface.id.invalid"
	CodeInterfaceIDDuplicate    Code = "interface.id.duplicate"
	CodeInterfaceNameRequired   Code = "interface.name.required"
	CodeInterfaceNameDuplicate  Code = "interface.name.duplicate"
	CodeInterfaceNameUnmatched  Code = "interface.name.unmatched"
	CodeInterfaceVLANMissing    Code = "interface.vlan.missing"
	CodeInterfaceVLANImplicit   Code = "interface.vlan.implicit"
	CodeInterfaceVLANCase       Code = "interface.vlan.case-mismatch"
	CodeInterfaceVLANUnknown    Code = "interface.vlan.unknown"
	CodeInterfaceNetworkNone    Code = "interface.network.unconnected"
	CodeInterfaceIPShared       Code = "interface.ip.shared"
	CodeInterfaceMACShared      Code = "interface.mac.shared"
	CodeInterfaceIPOutside      Code = "interface.ip.outside-subnet"
	CodeInterfaceRulesetUnknown Code = "interface.ruleset.unknown"
	CodeInterfaceOSPFOutside    Code = "interface.ospf.outside-area"
)

// Codes of the payload of a switch node.
const (
	CodeSwitchNetworkRequired Code = "switch.network.required"
	CodeSwitchNetworkUnknown  Code = "switch.network.unknown"
	CodeSwitchColorInvalid    Code = "switch.color.invalid"
	CodeSwitchIconSizeUnknown Code = "switch.icon-size.unknown"
	CodeSwitchLayerRange      Code = "switch.layer.out-of-range"
)

// Codes of the payload of a group node.
const (
	CodeGroupBorderStyleUnknown  Code = "group.border-style.unknown"
	CodeGroupIconKeyUnknown      Code = "group.icon-key.unknown"
	CodeGroupIconInvalid         Code = "group.icon.invalid"
	CodeGroupIconSizeUnknown     Code = "group.icon-size.unknown"
	CodeGroupHideDescriptionType Code = "group.hide-description.not-boolean"
)

// Codes of drawings: shape, icon and line nodes.
const (
	CodeDrawingShapeUnknown       Code = "drawing.shape.unknown"
	CodeDrawingColorInvalid       Code = "drawing.color.invalid"
	CodeDrawingBorderStyleUnknown Code = "drawing.border-style.unknown"
	CodeDrawingIconAmbiguous      Code = "drawing.icon.ambiguous"
	CodeDrawingIconKeyUnknown     Code = "drawing.icon-key.unknown"
	CodeDrawingIconInvalid        Code = "drawing.icon.invalid"
	CodeDrawingPointsTooFew       Code = "drawing.points.too-few"
	CodeDrawingPointsTooMany      Code = "drawing.points.too-many"
	CodeDrawingPointsNotFinite    Code = "drawing.points.not-finite"
	CodeDrawingLineStyleUnknown   Code = "drawing.line-style.unknown"
	CodeDrawingArrowNotBoolean    Code = "drawing.arrow.not-boolean"
)

// Codes of edges, the connections between a device interface and a switch.
const (
	CodeEdgeIDRequired       Code = "edge.id.required"
	CodeEdgeIDInvalid        Code = "edge.id.invalid"
	CodeEdgeIDDuplicate      Code = "edge.id.duplicate"
	CodeEdgeRouteTooFew      Code = "edge.route.too-few"
	CodeEdgeRouteNotFinite   Code = "edge.route.not-finite"
	CodeEdgeLineStyleUnknown Code = "edge.line-style.unknown"
	CodeEdgeSourceUnknown    Code = "edge.source.unknown"
	CodeEdgeTargetUnknown    Code = "edge.target.unknown"
	CodeEdgeEndpointsSame    Code = "edge.endpoints.same"
	CodeEdgeEndpointsInvalid Code = "edge.endpoints.invalid"
	CodeEdgeHandleUnknown    Code = "edge.handle.unknown"
	CodeEdgeInterfaceTaken   Code = "edge.interface.connected"
	CodeEdgeNetworkRequired  Code = "edge.network.required"
	CodeEdgeNetworkUnknown   Code = "edge.network.unknown"
	CodeEdgeNetworkMismatch  Code = "edge.network.mismatch"
)

// Codes of the Scenario configs a document lists.
const (
	CodeScenarioListTooMany   Code = "scenario.list.too-many"
	CodeScenarioNameRequired  Code = "scenario.name.required"
	CodeScenarioNameTooLong   Code = "scenario.name.too-long"
	CodeScenarioNameInvalid   Code = "scenario.name.invalid"
	CodeScenarioNameDuplicate Code = "scenario.name.duplicate"
)

// Codes of the layers a document defines.
const (
	CodeLayerListTooMany   Code = "layer.list.too-many"
	CodeLayerIDRange       Code = "layer.id.out-of-range"
	CodeLayerIDDuplicate   Code = "layer.id.duplicate"
	CodeLayerLabelRequired Code = "layer.label.required"
	CodeLayerLabelTooLong  Code = "layer.label.too-long"
	CodeLayerLabelControl  Code = "layer.label.control"

	CodeLayerIconKeyUnknown         Code = "layer.icon-key.unknown"
	CodeLayerIconInvalid            Code = "layer.icon.invalid"
	CodeLayerColorInvalid           Code = "layer.color.invalid"
	CodeLayerMatchersNotObject      Code = "layer.matchers.not-object"
	CodeLayerMatchersTooMany        Code = "layer.matchers.too-many"
	CodeLayerMatcherPathTooLong     Code = "layer.matcher-path.too-long"
	CodeLayerMatcherPathInvalid     Code = "layer.matcher-path.invalid"
	CodeLayerMatcherPatternNotText  Code = "layer.matcher-pattern.not-text"
	CodeLayerMatcherPatternRequired Code = "layer.matcher-pattern.required"
	CodeLayerMatcherPatternTooLong  Code = "layer.matcher-pattern.too-long"
	CodeLayerMatcherPatternInvalid  Code = "layer.matcher-pattern.invalid"
)

// Codes of layer sets: the options of the set a document copied its layers
// from, a set of the server's library, a layer set file, and the answers of
// the layer set routes.
const (
	CodeLayerSetNotObject            Code = "layerset.not-object"
	CodeLayerSetNameRequired         Code = "layerset.name.required"
	CodeLayerSetNameTooLong          Code = "layerset.name.too-long"
	CodeLayerSetNameControl          Code = "layerset.name.control"
	CodeLayerSetDescriptionTooLong   Code = "layerset.description.too-long"
	CodeLayerSetDescriptionControl   Code = "layerset.description.control"
	CodeLayerSetIconKeyUnknown       Code = "layerset.icon-key.unknown"
	CodeLayerSetIconInvalid          Code = "layerset.icon.invalid"
	CodeLayerSetMatchConflictUnknown Code = "layerset.match-conflict.unknown"
	CodeLayerSetOutlinesNotBoolean   Code = "layerset.layer-outlines.not-boolean"
	CodeLayerSetLayersEmpty          Code = "layerset.layers.empty"
	CodeLayerSetSourceIDInvalid      Code = "layerset.source-id.invalid"
	CodeLayerSetSourceVersionInvalid Code = "layerset.source-version.invalid"
	CodeLayerSetFileSchemaMismatch   Code = "layerset.file.schema-mismatch"
	CodeLayerSetInvalid              Code = "layerset.invalid"
	CodeLayerSetReadOnly             Code = "layerset.read-only"
	CodeLayerSetStale                Code = "layerset.stale"
	CodeLayerSetLibraryFull          Code = "layerset.library.full"
	CodeLayerSetDefaultUnknown       Code = "layerset.default.unknown"
)

// Codes of the document's source: the config it was generated from.
const (
	CodeSourceKindUnknown         Code = "source.kind.unknown"
	CodeSourceDigestMalformed     Code = "source.digest.malformed"
	CodeSourceAnnotationsNotMap   Code = "source.annotations.not-object"
	CodeSourceAnnotationsTooMany  Code = "source.annotations.too-many"
	CodeSourceAnnotationsTooLarge Code = "source.annotations.too-large"
	CodeSourceAnnotationNotText   Code = "source.annotation.not-text"
	CodeSourceAnnotationKeyBlank  Code = "source.annotation-key.blank"
	CodeSourceAnnotationKeyLong   Code = "source.annotation-key.too-long"
	CodeSourceAnnotationKeyCtrl   Code = "source.annotation-key.control"
)

// Codes of the included topologies a document's source names.
const (
	CodeIncludeListNotList    Code = "include.list.not-list"
	CodeIncludeNameRequired   Code = "include.name.required"
	CodeIncludeNameWhitespace Code = "include.name.whitespace"
)

// Codes of device templates, in a document, a library or a template file.
const (
	CodeTemplateListNotList           Code = "template.list.not-list"
	CodeTemplateListTooMany           Code = "template.list.too-many"
	CodeTemplateIDRequired            Code = "template.id.required"
	CodeTemplateIDInvalid             Code = "template.id.invalid"
	CodeTemplateIDDuplicate           Code = "template.id.duplicate"
	CodeTemplateNameRequired          Code = "template.name.required"
	CodeTemplateNameTooLong           Code = "template.name.too-long"
	CodeTemplateNameControl           Code = "template.name.control"
	CodeTemplateNameDuplicate         Code = "template.name.duplicate"
	CodeTemplateDescriptionTooLong    Code = "template.description.too-long"
	CodeTemplateDescriptionControl    Code = "template.description.control"
	CodeTemplateSpecRequired          Code = "template.spec.required"
	CodeTemplateHostnameRequired      Code = "template.hostname.required"
	CodeTemplateHostnameWhitespace    Code = "template.hostname.whitespace"
	CodeTemplateIconKeyUnknown        Code = "template.icon-key.unknown"
	CodeTemplateIconInvalid           Code = "template.icon.invalid"
	CodeTemplateIconSizeUnknown       Code = "template.icon-size.unknown"
	CodeTemplateColorInvalid          Code = "template.color.invalid"
	CodeTemplateLayerRange            Code = "template.layer.out-of-range"
	CodeTemplateHideDescriptionType   Code = "template.hide-description.not-boolean"
	CodeTemplateDeviceUnencodable     Code = "template.device.unencodable"
	CodeTemplateDeviceTooLarge        Code = "template.device.too-large"
	CodeTemplateSourceKindUnknown     Code = "template.source.kind-unknown"
	CodeTemplateSourceIDInvalid       Code = "template.source.id-invalid"
	CodeTemplateSourceVersionInvalid  Code = "template.source.version-invalid"
	CodeTemplateSourceDigestInvalid   Code = "template.source.digest-invalid"
	CodeTemplateFileInvalid           Code = "template.file.invalid"
	CodeTemplateFileSchemaMismatch    Code = "template.file.schema-mismatch"
	CodeTemplateFileEmpty             Code = "template.file.empty"
	CodeTemplateFileTooMany           Code = "template.file.too-many"
	CodeTemplateCollectionRequired    Code = "template.collection-name.required"
	CodeTemplateCollectionTooLong     Code = "template.collection-name.too-long"
	CodeTemplateCollectionControl     Code = "template.collection-name.control"
	CodeTemplateCollectionDescLong    Code = "template.collection-description.too-long"
	CodeTemplateCollectionDescControl Code = "template.collection-description.control"
	CodeTemplateItemReadOnly          Code = "template.item.read-only"
	CodeTemplateItemStale             Code = "template.item.stale"
)

// Codes of the custom icons a document or a template file carries.
const (
	CodeIconListNotObject Code = "icon.list.not-object"
	CodeIconListTooMany   Code = "icon.list.too-many"
	CodeIconNameInvalid   Code = "icon.name.invalid"
	CodeIconDataInvalid   Code = "icon.data.invalid"
	CodeIconPNGInvalid    Code = "icon.png.invalid"
)

// Codes of Builder packages: what makes a package unusable (see
// [Package.Validate]), and what the package of a document names but does
// not carry, or leaves out of its requirements (POST /builder/package).
const (
	CodePackageInvalid               Code = "package.invalid"
	CodePackageTooLarge              Code = "package.too-large"
	CodePackageSchemaMismatch        Code = "package.schema.mismatch"
	CodePackageDocumentMissing       Code = "package.document.missing"
	CodePackageDocumentInvalid       Code = "package.document.invalid"
	CodePackageConfigsTooMany        Code = "package.configs.too-many"
	CodePackageConfigKeyInvalid      Code = "package.config-key.invalid"
	CodePackageConfigKindMismatch    Code = "package.config-kind.mismatch"
	CodePackageConfigNameMismatch    Code = "package.config-name.mismatch"
	CodePackageConfigVersionInvalid  Code = "package.config-api-version.invalid"
	CodePackageConfigSpecMissing     Code = "package.config-spec.missing"
	CodePackageConfigUnlisted        Code = "package.config.unlisted"
	CodePackageConfigBuilderNote     Code = "package.config-annotation.builder"
	CodePackageRequirementsMissing   Code = "package.requirements.missing"
	CodePackageRequirementsTooMany   Code = "package.requirements.too-many"
	CodePackageRequirementBlank      Code = "package.requirement.blank"
	CodePackageRequirementTooLong    Code = "package.requirement.too-long"
	CodePackageRequirementControl    Code = "package.requirement.control"
	CodePackageConfigUnreadable      Code = "package.config.unreadable"
	CodePackageIncludeFilePath       Code = "package.include.file-path"
	CodePackageIconMissing           Code = "package.icon.missing"
	CodePackageIconsTooMany          Code = "package.icons.too-many"
	CodePackageRequirementLeftOut    Code = "package.requirement.left-out"
	CodePackageRequirementsTruncated Code = "package.requirements.truncated"
)

// Codes of publishing: why a draft or a document is not published, and what
// a publication that went through warns of.
const (
	CodePublishBlocked              Code = "publish.blocked"
	CodePublishTopologyInvalid      Code = "publish.topology.invalid"
	CodePublishTopologyExists       Code = "publish.topology.exists"
	CodePublishTopologyMissing      Code = "publish.topology.missing"
	CodePublishTopologyNotSource    Code = "publish.topology.not-source"
	CodePublishTopologyChanged      Code = "publish.topology.changed"
	CodePublishTopologyFileMismatch Code = "publish.topology.file-mismatch"
	CodePublishTopologyFileChanged  Code = "publish.topology.file-changed"
	CodePublishExperimentExists     Code = "publish.experiment.exists"
	CodePublishExperimentMissing    Code = "publish.experiment.missing"
	CodePublishExperimentNotSource  Code = "publish.experiment.not-source"
	CodePublishExperimentChanged    Code = "publish.experiment.changed"
	CodePublishExperimentRunning    Code = "publish.experiment.running"
	CodePublishExperimentInvalid    Code = "publish.experiment.invalid"
	CodePublishExperimentReserved   Code = "publish.experiment.reserved"
	CodePublishExperimentNameLong   Code = "publish.experiment.name-too-long"
	CodePublishExperimentUnmerged   Code = "publish.experiment.unmergeable"
	CodePublishSourceChanged        Code = "publish.source.changed"
	CodePublishScenarioMissing      Code = "publish.scenario.missing"
	CodePublishScenarioNotListed    Code = "publish.scenario.not-listed"
	CodePublishScenarioInvalid      Code = "publish.scenario.invalid"
	CodePublishIncludeClash         Code = "publish.include.clash"
	CodePublishIncludeHostname      Code = "publish.include.hostname"
	CodePublishTargetInvalid        Code = "publish.target.invalid"
	CodePublishStageFailed          Code = "publish.stage.failed"
	CodePublishCleanupFailed        Code = "publish.cleanup.failed"
	CodePublishBroadcastFailed      Code = "publish.broadcast.failed"
	CodePublishFileUnchanged        Code = "publish.file.unchanged"
	CodePublishLegacyReplaced       Code = "publish.legacy.replaced"
	CodePublishLegacyRemoved        Code = "publish.legacy.removed"
	CodePublishExperimentUnrecorded Code = "publish.experiment.unrecorded"
	CodePublishExperimentPins       Code = "publish.experiment.pins-removed"
	CodePublishScenarioPartial      Code = "publish.scenario.partial"
	CodePublishRetryComplete        Code = "publish.retry.complete"
	CodePublishRetryAvailable       Code = "publish.retry.available"
)

// Codes of the preflight checks a draft can be put through before an
// experiment is started from it: host capacity, networks, disk images and
// scenario apps.
const (
	CodePreflightCapacityCPU       Code = "preflight.capacity.cpu"
	CodePreflightCapacityMemory    Code = "preflight.capacity.memory"
	CodePreflightCapacityVMTooBig  Code = "preflight.capacity.vm-too-large"
	CodePreflightNetworkVLANRange  Code = "preflight.network.vlan-range"
	CodePreflightNetworkAliasInUse Code = "preflight.network.alias-in-use"
	CodePreflightNetworkBridge     Code = "preflight.network.bridge-missing"
	CodePreflightDiskMissing       Code = "preflight.disk.missing"
	CodePreflightDiskKind          Code = "preflight.disk.kind"
	CodePreflightAppMissing        Code = "preflight.app.missing"
	CodePreflightScenarioMissing   Code = "preflight.app.scenario-missing"
	CodePreflightScenarioUnread    Code = "preflight.app.scenario-unreadable"
	CodePreflightUnavailable       Code = "preflight.unavailable"
)

// Codes of the answers about a draft that a client tells apart: a version
// of it that is no longer current, a change made while the request was
// handled, a version that cannot be deleted, and a new draft past the most
// the server holds.
const (
	CodeDraftStale           Code = "draft.stale"
	CodeDraftConflict        Code = "draft.conflict"
	CodeDraftSnapshotCurrent Code = "draft.snapshot.current"
	CodeDraftLimit           Code = "draft.limit"
)

// Codes of importing a config as a draft (POST /builder/generate).
const (
	CodeImportNameInvalid Code = "import.name.invalid"
	CodeImportNameSource  Code = "import.name.source"
)

// Codes of Builder REST errors that no rule above names: what is wrong with
// the request, or with the server.
const (
	CodeRequestInvalid          Code = "request.invalid"
	CodeRequestForbidden        Code = "request.forbidden"
	CodeRequestNotFound         Code = "request.not-found"
	CodeRequestMethodNotAllowed Code = "request.method-not-allowed"
	CodeRequestConflict         Code = "request.conflict"
	CodeRequestStale            Code = "request.stale"
	CodeRequestTooLarge         Code = "request.too-large"
	CodeRequestUnprocessable    Code = "request.unprocessable"
	CodeServerError             Code = "server.error"
	CodeServerBusy              Code = "server.busy"
)

// codeRegistry is every code, grouped by what it is about, each with its
// default severity and the rule it names.
//
//nolint:gochecknoglobals // the registry is one table, read only through LookupCode and the tests
var codeRegistry = []CodeInfo{
	{CodeDocumentInvalid, SeverityError, "The document does not validate. The issues field lists each reason."},
	{CodeDocumentContentMissing, SeverityError, "There is no document at all."},
	{CodeDocumentSchemaMismatch, SeverityError, "The document's $schema is not the Builder document schema."},
	{CodeDocumentRevisionMismatch, SeverityError, "The document's revision is not the one this phenix reads."},
	{CodeDocumentListMissing, SeverityError, "nodes, networks or edges is missing, null or not a list."},
	{CodeDocumentViewportNotFinite, SeverityError, "A viewport value is not a finite number."},
	{CodeDocumentZoomNotPositive, SeverityError, "The viewport zoom is not a positive number."},
	{CodeDocumentGridInvalid, SeverityError, "The grid size is not a whole number from 4 to 200."},
	{CodeDocumentIconSizeUnknown, SeverityError, "The document's icon size is not small, medium or large."},
	{CodeDocumentLayoutNotText, SeverityError, "The document's layout is not text."},
	{CodeDocumentFieldUnknown, SeverityError, "An object of the document holds a key that is not one of its fields."},

	{CodeMetadataNotObject, SeverityError, "The document's metadata is not an object."},
	{CodeMetadataIDRequired, SeverityError, "The document has no ID."},
	{CodeMetadataIDInvalid, SeverityError, "The document ID is not a UUID."},
	{CodeMetadataNameTooLong, SeverityError, "The document name is longer than 512 bytes."},
	{CodeMetadataNameControl, SeverityError, "The document name holds a control character."},
	{CodeMetadataUserNotText, SeverityError, "createdBy or updatedBy is not text."},
	{CodeMetadataUserTooLong, SeverityError, "createdBy or updatedBy is longer than 256 bytes."},
	{CodeMetadataUserControl, SeverityError, "createdBy or updatedBy holds a control character."},
	{CodeMetadataTimeNotText, SeverityError, "createdAt or updatedAt is not text."},
	{CodeMetadataTimeInvalid, SeverityError, "createdAt or updatedAt is not a UTC time of the form YYYY-MM-DDTHH:MM:SSZ."},
	{CodeMetadataNotesNotList, SeverityError, "The diagram's notes are not a list of text."},
	{CodeMetadataNotesTooMany, SeverityError, "The diagram has more than 100 notes."},
	{CodeMetadataNoteNotText, SeverityError, "A note of the diagram is not text."},
	{CodeMetadataNoteBlank, SeverityError, "A note of the diagram is blank."},
	{CodeMetadataNoteTooLong, SeverityError, "A note of the diagram is longer than 4096 bytes."},
	{CodeMetadataNoteControl, SeverityError, "A note of the diagram holds a control character other than newline and tab."},

	{CodeNetworkIDRequired, SeverityError, "A network has no ID."},
	{CodeNetworkIDInvalid, SeverityError, "A network ID is not a UUID."},
	{CodeNetworkIDDuplicate, SeverityError, "Two networks have the same ID, ignoring case."},
	{CodeNetworkNameRequired, SeverityError, "A network has no name."},
	{CodeNetworkNameWhitespace, SeverityError, "A network name holds white space."},
	{CodeNetworkNameDuplicate, SeverityError, "Two networks have exactly the same name."},
	{CodeNetworkLineStyle, SeverityError, "A network's line style is not one the Builder draws."},
	{CodeNetworkAliasRange, SeverityError, "A network's VLAN alias is not from 1 to 4094."},
	{CodeNetworkAliasDuplicate, SeverityError, "Two networks have the same VLAN alias."},
	{CodeNetworkSwitchMissing, SeverityWarning, "A network has no switch on the canvas."},
	{CodeNetworkSubnetInvalid, SeverityError, "A network's subnet is not an IPv4 network such as 10.1.0.0/24."},
	{CodeNetworkGatewayInvalid, SeverityError, "A network's gateway is not an IPv4 address."},
	{CodeNetworkGatewayOutside, SeverityError, "A network's gateway is not a host address of its subnet."},
	{CodeNetworkGatewayUnowned, SeverityWarning, "No interface on a network has the network's gateway address."},
	{CodeNetworkSubnetOverlap, SeverityWarning, "The subnets of two networks overlap. " +
		"It is a problem only for networks that connect, not for isolated enclaves that reuse a subnet on purpose."},

	{CodeNodeIDRequired, SeverityError, "A node has no ID."},
	{CodeNodeIDInvalid, SeverityError, "A node ID is not a UUID."},
	{CodeNodeIDDuplicate, SeverityError, "Two nodes have the same ID, ignoring case."},
	{CodeNodePositionNotFinite, SeverityError, "A node's position is not finite numbers."},
	{CodeNodeSizeInvalid, SeverityError, "A node's size is not positive finite numbers."},
	{CodeNodeKindUnknown, SeverityError, "A node's kind is not one the Builder knows."},
	{CodeNodePayloadMissing, SeverityError, "A node lacks the payload of its kind."},
	{CodeNodePayloadExtra, SeverityError, "A node carries the payload of another kind."},
	{CodeNodeHostnameRequired, SeverityError, "A device has no hostname."},
	{CodeNodeHostnameWhitespace, SeverityError, "A device hostname holds white space."},
	{CodeNodeHostnameDuplicate, SeverityError, "Two devices have the same hostname, ignoring case."},
	{CodeNodeHostnameShort, SeverityError, "A device hostname is 1 character long, which phenix refuses."},
	{CodeNodeHostnameReserved, SeverityError, `A device hostname is "all", which minimega takes for every VM.`},
	{CodeNodeHostnameNumeric, SeverityError, "A device hostname is all digits, which phenix refuses in an experiment."},
	{CodeNodeHostnameWindows, SeverityError, `A Windows device has the hostname "phenix", which phenix refuses in an experiment.`},
	{CodeNodeHostnameReservedCase, SeverityWarning, `A device hostname differs from "all" only by case.`},
	{
		CodeNodeHostnamePhenix,
		SeverityWarning,
		`A device that is not Windows has the hostname "phenix", the hostname of phenix's own images.`,
	},
	{CodeNodeParentSelf, SeverityError, "A node is its own parent."},
	{CodeNodeParentUnknown, SeverityError, "A node's parent is no node of the document."},
	{CodeNodeParentNotGroup, SeverityError, "A node's parent is not a group."},
	{CodeNodeParentCycle, SeverityError, "Groups contain each other in a cycle."},

	{CodeDeviceSpecRequired, SeverityError, "A device has no spec."},
	{CodeDeviceHostnameMismatch, SeverityWarning, "The spec of a device names a hostname other than the device hostname. " +
		"The topology uses the device hostname."},
	{CodeDeviceIconKeyUnknown, SeverityError, "A device's icon key is not a built-in icon."},
	{CodeDeviceIconInvalid, SeverityError, "A device's custom icon is not an icon name."},
	{CodeDeviceIconSizeUnknown, SeverityError, "A device's icon size is not small, medium or large."},
	{CodeDeviceColorInvalid, SeverityError, "A device's outline or fill color is not #rrggbb."},
	{CodeDeviceLayerRange, SeverityError, "A device's layer is not an integer from 0 to 99."},
	{CodeDeviceHideDescriptionType, SeverityError, "A device's hideDescription is not true or false."},
	{CodeDeviceIncludedFromInvalid, SeverityError, "A device names an included topology, but the name is blank or holds white space."},
	{CodeDeviceIncludedFromNoSource, SeverityError, "A device names an included topology, but the document includes no topology."},
	{CodeDeviceInterfacesNone, SeverityWarning, "A device has no interfaces."},
	{CodeDeviceDrivesNone, SeverityError, "A device phenix starts has no drive, which blocks publishing."},
	{CodeDeviceImageUnknown, SeverityWarning, "A device's drive image is not one of the server's disk images."},
	{CodeDeviceRulesetsIgnored, SeverityWarning, "A device that is not a Router or a Firewall has firewall rulesets, " +
		"which phenix does not apply."},
	{CodeDeviceRouterOSUnsupported, SeverityWarning, "A Router or Firewall has an os_type other than vyatta, vyos " +
		"or minirouter, so phenix does not configure it as a router."},
	{CodeDeviceInterfacesUnset, SeverityWarning, "A device has static interfaces, but its os_type is one for which " +
		"phenix writes no interface configuration."},
	{CodeDeviceSerialUnset, SeverityWarning, "A device has serial interfaces, but phenix sets them up only when " +
		"its os_type is linux."},
	{CodeDeviceInjectionsUnapplied, SeverityWarning, "A device has file injections or deletions that phenix does " +
		"not apply, because its snapshot is off or its first drive's inject partition is 0."},
	{CodeDeviceOSPFMissing, SeverityError, "A Vyatta or VyOS router has an interface with proto ospf but no " +
		"network.ospf, so the experiment fails to start, which blocks publishing."},
	{CodeDeviceOSPFUnused, SeverityWarning, "A minirouter has network.ospf, but none of its interfaces has proto ospf."},
	{CodeDeviceOSPFNetworkUnused, SeverityWarning, "An OSPF area network of a minirouter holds the address of " +
		"none of its interfaces with proto ospf, so minirouter does not advertise it."},
	{CodeDeviceNTPServerInvalid, SeverityError, "The ntp-server label names no interface of its device, or one " +
		"with no IP address, so the experiment fails to start, which blocks publishing."},
	{CodeDeviceNTPServerIgnored, SeverityWarning, "More than one device has the ntp-server label, and phenix " +
		"uses only the first."},
	{CodeDeviceNTPServerNoMgmt, SeverityWarning, "Routers get their NTP server from an interface of the " +
		"ntp-server device on VLAN mgmt, and it has none."},
	{CodeDeviceEnableSSHInvalid, SeverityWarning, "The vrouter/enable-ssh annotation of a Vyatta or VyOS router " +
		"is neither an IP address nor the name of an interface with an address."},
	{CodeDeviceAutotunnelInvalid, SeverityWarning, "The phenix/startup-autotunnel annotation is not a list of " +
		"\"port\", \"port:dport\" or \"port:host:dport\" texts."},
	{CodeDeviceDefaultAppsInvalid, SeverityWarning, "The phenix/default-apps annotation is not true or false."},

	{CodeInterfaceIDRequired, SeverityError, "A connection point has no ID."},
	{CodeInterfaceIDInvalid, SeverityError, "A connection point ID is not a UUID."},
	{CodeInterfaceIDDuplicate, SeverityError, "Two connection points have the same ID, ignoring case."},
	{CodeInterfaceNameRequired, SeverityError, "A connection point has no name."},
	{CodeInterfaceNameDuplicate, SeverityError, "Two connection points of a device have the same name, ignoring case."},
	{CodeInterfaceNameUnmatched, SeverityWarning, "A connection point names no interface of the device's spec, so publishing drops it."},
	{CodeInterfaceVLANMissing, SeverityError, "An interface of a device phenix starts has no VLAN, which blocks publishing."},
	{CodeInterfaceVLANImplicit, SeverityWarning, "A publish puts an unconnected interface on the network of the diagram " +
		"that its VLAN names."},
	{CodeInterfaceVLANCase, SeverityWarning, "An interface's VLAN differs from a network's name only in case, " +
		"and phenix does not match them."},
	{CodeInterfaceVLANUnknown, SeverityWarning, "An interface's VLAN names no network of the diagram."},
	{CodeInterfaceNetworkNone, SeverityWarning, "An interface is not connected to a network."},
	{CodeInterfaceIPShared, SeverityError, "Interfaces on one network use the same IP address, which blocks publishing."},
	{CodeInterfaceMACShared, SeverityError, "Interfaces on one network use the same MAC address, which blocks publishing."},
	{CodeInterfaceIPOutside, SeverityWarning, "An interface's IP address is not a host address of the subnet of its network."},
	{CodeInterfaceRulesetUnknown, SeverityWarning, "An interface names a firewall ruleset that its device does not have, " +
		"so phenix applies no firewall to that interface."},
	{CodeInterfaceOSPFOutside, SeverityWarning, "An interface with proto ospf of a minirouter is in no OSPF " +
		"area network of the device, or the device has no network.ospf. As a result, the interface is in no OSPF area."},

	{CodeSwitchNetworkRequired, SeverityError, "A switch names no network."},
	{CodeSwitchNetworkUnknown, SeverityError, "A switch names a network the document does not have."},
	{CodeSwitchColorInvalid, SeverityError, "A switch's outline or fill color is not #rrggbb."},
	{CodeSwitchIconSizeUnknown, SeverityError, "A switch's icon size is not small, medium or large."},
	{CodeSwitchLayerRange, SeverityError, "A switch's layer is not an integer from 0 to 99."},

	{CodeGroupBorderStyleUnknown, SeverityError, "A group's border style is not one the Builder draws."},
	{CodeGroupIconKeyUnknown, SeverityError, "A group's icon key is not a built-in icon."},
	{CodeGroupIconInvalid, SeverityError, "A group's custom icon is not an icon name."},
	{CodeGroupIconSizeUnknown, SeverityError, "A group's icon size is not small, medium or large."},
	{CodeGroupHideDescriptionType, SeverityError, "A group's hideDescription is not true or false."},

	{CodeDrawingShapeUnknown, SeverityError, "A shape's figure is not rectangle or circle."},
	{CodeDrawingColorInvalid, SeverityError, "A shape's or a line's color is not #rrggbb."},
	{CodeDrawingBorderStyleUnknown, SeverityError, "A shape's border style is not one the Builder draws."},
	{CodeDrawingIconAmbiguous, SeverityError, "An icon drawing names both or neither of a built-in icon and a custom icon."},
	{CodeDrawingIconKeyUnknown, SeverityError, "An icon drawing's icon key is not a built-in icon."},
	{CodeDrawingIconInvalid, SeverityError, "An icon drawing's custom icon is not an icon name."},
	{CodeDrawingPointsTooFew, SeverityError, "A line has fewer than 2 points."},
	{CodeDrawingPointsTooMany, SeverityError, "A line has more than 64 points."},
	{CodeDrawingPointsNotFinite, SeverityError, "A point of a line is not finite numbers."},
	{CodeDrawingLineStyleUnknown, SeverityError, "A line's style is not one the Builder draws."},
	{CodeDrawingArrowNotBoolean, SeverityError, "A line's startArrow or endArrow is not true or false."},

	{CodeEdgeIDRequired, SeverityError, "A connection has no ID."},
	{CodeEdgeIDInvalid, SeverityError, "A connection ID is not a UUID."},
	{CodeEdgeIDDuplicate, SeverityError, "Two connections have the same ID, ignoring case."},
	{CodeEdgeRouteTooFew, SeverityError, "A connection's route has fewer than 2 points."},
	{CodeEdgeRouteNotFinite, SeverityError, "A point of a connection's route is not finite numbers."},
	{CodeEdgeLineStyleUnknown, SeverityError, "A connection's line style is not one the Builder draws."},
	{CodeEdgeSourceUnknown, SeverityError, "A connection's source is no node of the document."},
	{CodeEdgeTargetUnknown, SeverityError, "A connection's target is no node of the document."},
	{CodeEdgeEndpointsSame, SeverityError, "A connection joins a node to itself."},
	{CodeEdgeEndpointsInvalid, SeverityError, "A connection does not join one device interface to one switch."},
	{CodeEdgeHandleUnknown, SeverityError, "A connection names a connection point its device does not have."},
	{CodeEdgeInterfaceTaken, SeverityError, "More than one connection attaches to an interface."},
	{CodeEdgeNetworkRequired, SeverityError, "A connection names no network."},
	{CodeEdgeNetworkUnknown, SeverityError, "A connection names a network the document does not have."},
	{CodeEdgeNetworkMismatch, SeverityError, "A connection names a network other than the network of its switch."},

	{CodeScenarioListTooMany, SeverityError, "The document lists more than 20 scenarios."},
	{CodeScenarioNameRequired, SeverityError, "A listed scenario has no name."},
	{CodeScenarioNameTooLong, SeverityError, "A scenario name is longer than 256 bytes."},
	{CodeScenarioNameInvalid, SeverityError, "A scenario name is not a config name."},
	{CodeScenarioNameDuplicate, SeverityError, "The document lists a scenario twice, ignoring case."},

	{CodeLayerListTooMany, SeverityError, "The document defines more than 32 layers."},
	{CodeLayerIDRange, SeverityError, "A layer ID is not an integer from 0 to 99."},
	{CodeLayerIDDuplicate, SeverityError, "Two layers have the same ID."},
	{CodeLayerLabelRequired, SeverityError, "A layer has no label."},
	{CodeLayerLabelTooLong, SeverityError, "A layer label is longer than 64 bytes."},
	{CodeLayerLabelControl, SeverityError, "A layer label holds a control character."},
	{CodeLayerIconKeyUnknown, SeverityError, "A layer's icon key is not a built-in icon."},
	{CodeLayerIconInvalid, SeverityError, "A layer's custom icon is not an icon name."},
	{CodeLayerColorInvalid, SeverityError, "A layer's color is not #rrggbb."},
	{CodeLayerMatchersNotObject, SeverityError, "A layer's node matchers are not an object of patterns by field."},
	{CodeLayerMatchersTooMany, SeverityError, "A layer has more than 16 node matchers."},
	{CodeLayerMatcherPathTooLong, SeverityError, "A node matcher field is longer than 128 bytes."},
	{CodeLayerMatcherPathInvalid, SeverityError, "A node matcher field is not keys of letters, digits, _ or - joined by dots."},
	{CodeLayerMatcherPatternNotText, SeverityError, "A node matcher pattern is not text."},
	{CodeLayerMatcherPatternRequired, SeverityError, "A node matcher has no pattern."},
	{CodeLayerMatcherPatternTooLong, SeverityError, "A node matcher pattern is longer than 256 bytes."},
	{
		CodeLayerMatcherPatternInvalid,
		SeverityError,
		"A node matcher pattern is not a regular expression, or it has a repeat that Builder refuses. " +
			"Builder refuses a repeated group that holds a repeat or an alternation, such as (a+)+. " +
			"It also refuses more than 3 *, + or {n,} repeats.",
	},

	{CodeLayerSetNotObject, SeverityError, "The document's layer set options are not an object."},
	{CodeLayerSetNameRequired, SeverityError, "A layer set has no name."},
	{CodeLayerSetNameTooLong, SeverityError, "A layer set name is longer than 128 bytes."},
	{CodeLayerSetNameControl, SeverityError, "A layer set name holds a control character."},
	{CodeLayerSetDescriptionTooLong, SeverityError, "A layer set description is longer than 1024 bytes."},
	{CodeLayerSetDescriptionControl, SeverityError, "A layer set description holds a control character."},
	{CodeLayerSetIconKeyUnknown, SeverityError, "A layer set's icon key is not a built-in icon."},
	{CodeLayerSetIconInvalid, SeverityError, "A layer set's custom icon is not an icon name."},
	{CodeLayerSetMatchConflictUnknown, SeverityError, "A layer set's match conflict rule is not lowest or highest."},
	{CodeLayerSetOutlinesNotBoolean, SeverityError, "The layer outlines option of a layer set is not true or false."},
	{CodeLayerSetLayersEmpty, SeverityError, "A layer set holds no layer."},
	{CodeLayerSetSourceIDInvalid, SeverityError, "The layer set ID that a document names is not a layer set ID."},
	{CodeLayerSetSourceVersionInvalid, SeverityError, "The layer set version that a document names is not an integer of 1 or more."},
	{CodeLayerSetFileSchemaMismatch, SeverityError, "A layer set file's $schema is not the layer set file schema."},
	{CodeLayerSetInvalid, SeverityError, "A layer set does not validate. The issues field lists each reason."},
	{CodeLayerSetReadOnly, SeverityError, "A request cannot change or delete a built-in layer set. Duplicate it to change it."},
	{CodeLayerSetStale, SeverityError, "The If-Match entity tag names a version of the layer set that is no longer current."},
	{CodeLayerSetLibraryFull, SeverityError, "The layer set library holds 200 sets, the most it may."},
	{CodeLayerSetDefaultUnknown, SeverityError, "The layer set chosen as the default does not exist."},

	{CodeSourceKindUnknown, SeverityError, "The source's kind is not manual, topology or experiment."},
	{CodeSourceDigestMalformed, SeverityError, "The source digest is not sha256: and 64 hex digits."},
	{CodeSourceAnnotationsNotMap, SeverityError, "The source annotations are not an object of text values."},
	{CodeSourceAnnotationsTooMany, SeverityError, "The source carries more than 100 annotations."},
	{CodeSourceAnnotationsTooLarge, SeverityError, "The source annotations take more than 256 KiB."},
	{CodeSourceAnnotationNotText, SeverityError, "A source annotation's value is not text."},
	{CodeSourceAnnotationKeyBlank, SeverityError, "A source annotation key is blank."},
	{CodeSourceAnnotationKeyLong, SeverityError, "A source annotation key is longer than 512 bytes."},
	{CodeSourceAnnotationKeyCtrl, SeverityError, "A source annotation key holds a control character."},

	{CodeIncludeListNotList, SeverityError, "The included topologies are not a list of names."},
	{CodeIncludeNameRequired, SeverityError, "An included topology has no name."},
	{CodeIncludeNameWhitespace, SeverityError, "The name of an included topology holds white space."},

	{CodeTemplateListNotList, SeverityError, "The document's templates are not a list."},
	{CodeTemplateListTooMany, SeverityError, "The document carries more than 50 templates."},
	{CodeTemplateIDRequired, SeverityError, "A template has no ID."},
	{CodeTemplateIDInvalid, SeverityError, "A template ID is not a UUID."},
	{CodeTemplateIDDuplicate, SeverityError, "Two templates have the same ID, ignoring case."},
	{CodeTemplateNameRequired, SeverityError, "A template has no name."},
	{CodeTemplateNameTooLong, SeverityError, "A template name is longer than 128 bytes."},
	{CodeTemplateNameControl, SeverityError, "A template name holds a control character."},
	{CodeTemplateNameDuplicate, SeverityError, "Two templates of a template file have the same name, ignoring case."},
	{CodeTemplateDescriptionTooLong, SeverityError, "A template description is longer than 1024 bytes."},
	{CodeTemplateDescriptionControl, SeverityError, "A template description holds a control character."},
	{CodeTemplateSpecRequired, SeverityError, "A template's device has no spec."},
	{CodeTemplateHostnameRequired, SeverityError, "A template's spec has no general.hostname."},
	{CodeTemplateHostnameWhitespace, SeverityError, "A template's hostname holds white space."},
	{CodeTemplateIconKeyUnknown, SeverityError, "A template's icon key is not a built-in icon."},
	{CodeTemplateIconInvalid, SeverityError, "A template's custom icon is not an icon name."},
	{CodeTemplateIconSizeUnknown, SeverityError, "A template's icon size is not small, medium or large."},
	{CodeTemplateColorInvalid, SeverityError, "A template's outline or fill color is not #rrggbb."},
	{CodeTemplateLayerRange, SeverityError, "A template's layer is not an integer from 0 to 99."},
	{CodeTemplateHideDescriptionType, SeverityError, "A template's hideDescription is not true or false."},
	{CodeTemplateDeviceUnencodable, SeverityError, "Builder cannot encode a template's device as JSON."},
	{CodeTemplateDeviceTooLarge, SeverityError, "A template's device takes more than 16 KiB as JSON."},
	{CodeTemplateSourceKindUnknown, SeverityError, "A template's source kind is not library, builtin or preloaded."},
	{CodeTemplateSourceIDInvalid, SeverityError, "A template's source has no ID, or an ID that is not a template ID."},
	{CodeTemplateSourceVersionInvalid, SeverityError, "A template's source version is less than 1."},
	{CodeTemplateSourceDigestInvalid, SeverityError, "A template's source digest is not sha256 and 64 lowercase hex digits."},
	{CodeTemplateFileInvalid, SeverityError, "A template file does not validate. The issues field lists each reason."},
	{CodeTemplateFileSchemaMismatch, SeverityError, "A template file's $schema is not the template file schema."},
	{CodeTemplateFileEmpty, SeverityError, "A template file holds no template."},
	{CodeTemplateFileTooMany, SeverityError, "A template file holds more than 200 templates."},
	{CodeTemplateCollectionRequired, SeverityError, "A template file's collection has no name."},
	{CodeTemplateCollectionTooLong, SeverityError, "A collection name is longer than 128 bytes."},
	{CodeTemplateCollectionControl, SeverityError, "A collection name holds a control character."},
	{CodeTemplateCollectionDescLong, SeverityError, "A collection description is longer than 1024 bytes."},
	{CodeTemplateCollectionDescControl, SeverityError, "A collection description holds a control character."},
	{CodeTemplateItemReadOnly, SeverityError, "A request changes a built-in template or an item of a template file. These are read only."},
	{CodeTemplateItemStale, SeverityError, "A template or collection changed after the request read it. Reload the template library."},

	{CodeIconListNotObject, SeverityError, "The custom icons are not an object of icons by name."},
	{CodeIconListTooMany, SeverityError, "The document carries more than 50 custom icons."},
	{CodeIconNameInvalid, SeverityError, "A custom icon's name is not an icon name."},
	{CodeIconDataInvalid, SeverityError, "A custom icon's data is not base64 of at most 40960 bytes."},
	{CodeIconPNGInvalid, SeverityError, "A custom icon is not a PNG the Builder accepts."},

	{
		CodePackageInvalid,
		SeverityError,
		"A Builder package does not decode or validate. The issues field lists each item that does not validate.",
	},
	{CodePackageTooLarge, SeverityError, "A Builder package is larger than 5 MiB."},
	{CodePackageSchemaMismatch, SeverityError, "A package's $schema is not the Builder package schema."},
	{CodePackageDocumentMissing, SeverityError, "A package holds no Builder document."},
	{CodePackageDocumentInvalid, SeverityError, "A package's document does not decode as a Builder document."},
	{CodePackageConfigsTooMany, SeverityError, "A package carries more than 20 Scenario configs or 100 Topology configs."},
	{CodePackageConfigKeyInvalid, SeverityError, "The key a package keeps a config under is not a config name."},
	{CodePackageConfigKindMismatch, SeverityError, "A carried config's kind is not the kind of its list."},
	{CodePackageConfigNameMismatch, SeverityError, "A carried config's metadata.name is not the key the package keeps it under."},
	{CodePackageConfigVersionInvalid, SeverityError, "A carried config's apiVersion is not phenix.sandia.gov/v and a number."},
	{CodePackageConfigSpecMissing, SeverityError, "A carried config has no spec."},
	{CodePackageConfigUnlisted, SeverityError, "A package carries a config its requirements do not name."},
	{CodePackageConfigBuilderNote, SeverityError, "A carried config has a Builder annotation, which a package never carries."},
	{CodePackageRequirementsMissing, SeverityError, "A list of a package's requirements is missing or null."},
	{CodePackageRequirementsTooMany, SeverityError, "A list of a package's requirements holds more than 1000 entries."},
	{CodePackageRequirementBlank, SeverityError, "An entry of a package's requirements is blank."},
	{CodePackageRequirementTooLong, SeverityError, "An entry of a package's requirements is longer than 4096 bytes."},
	{CodePackageRequirementControl, SeverityError, "An entry of a package's requirements holds a control character."},
	{CodePackageConfigUnreadable, SeverityWarning, "A Scenario config or included topology that the document names " +
		"does not exist, or the caller may not read it. The package does not carry it."},
	{CodePackageIncludeFilePath, SeverityWarning, "An included topology is a file path, which a package never carries."},
	{CodePackageIconMissing, SeverityWarning, "A custom icon the document names is not in the icon library, " +
		"so the package does not carry it."},
	{CodePackageIconsTooMany, SeverityWarning, "The document names more custom icons than the 50 a package carries. " +
		"The package does not carry the rest."},
	{CodePackageRequirementLeftOut, SeverityWarning, "A name or path that the document or a config holds is blank, longer " +
		"than 4096 bytes or holds a control character. The package does not list it."},
	{CodePackageRequirementsTruncated, SeverityWarning, "A list of the requirements would hold more than 1000 entries, " +
		"so the package lists the first 1000."},

	{CodePublishBlocked, SeverityError, "Only publishing refuses the topology. " +
		"The issues field lists each interface, address or hostname to fix."},
	{CodePublishTopologyInvalid, SeverityError, "phenix's topology schema refuses the topology the document publishes as."},
	{CodePublishTopologyExists, SeverityError, "The topology a publish creates already exists."},
	{CodePublishTopologyMissing, SeverityError, "The topology a publish updates does not exist."},
	{CodePublishTopologyNotSource, SeverityError, "The topology is not the source of this draft, so the draft cannot update it."},
	{CodePublishTopologyChanged, SeverityError, "The topology changed after this draft published it."},
	{CodePublishTopologyFileMismatch, SeverityError, "The topology is not what its Builder file publishes, so a publish cannot update it."},
	{CodePublishTopologyFileChanged, SeverityError, "The topology or its Builder file changed after Builder opened " +
		"this draft from the file."},
	{CodePublishExperimentExists, SeverityError, "The experiment a publish creates already exists."},
	{CodePublishExperimentMissing, SeverityError, "The experiment a publish updates does not exist."},
	{CodePublishExperimentNotSource, SeverityError, "The experiment is not the source of this draft, so the draft cannot update it."},
	{CodePublishExperimentChanged, SeverityError, "The experiment changed after this draft published it."},
	{CodePublishExperimentRunning, SeverityError, "A publish cannot update a running experiment."},
	{CodePublishExperimentInvalid, SeverityError, "phenix refuses the experiment a publish would write."},
	{CodePublishExperimentReserved, SeverityError, `The experiment is named "all", which phenix reserves.`},
	{CodePublishExperimentNameLong, SeverityError, "This server names a bridge after the experiment, " +
		"and the experiment name is too long for a bridge name."},
	{CodePublishExperimentUnmerged, SeverityError, "An update cannot merge an included topology into the experiment that it writes."},
	{CodePublishSourceChanged, SeverityError, "The topology or experiment that is the source of a diagram changed " +
		"on the server after Builder made the diagram."},
	{CodePublishScenarioMissing, SeverityError, "A scenario the document lists does not exist, or the caller may not read it."},
	{CodePublishScenarioNotListed, SeverityError, "The experiment's scenario is not one the document lists."},
	{CodePublishScenarioInvalid, SeverityError, "A listed scenario is not valid if the publish adds the topology."},
	{CodePublishIncludeClash, SeverityError, "The topology and a topology that it includes have the same hostname."},
	{CodePublishIncludeHostname, SeverityError, "An included topology has a hostname phenix refuses in an experiment."},
	{CodePublishTargetInvalid, SeverityError, "A topology, experiment or scenario name is not a config name."},
	{CodePublishStageFailed, SeverityError, "A publish stage failed after the server wrote earlier stages."},
	{CodePublishCleanupFailed, SeverityWarning, "The publication is complete, but the server could not remove " +
		"the content that it replaces."},
	{CodePublishBroadcastFailed, SeverityWarning, "The server stored a config, but it could not broadcast the live update of the config."},
	{CodePublishFileUnchanged, SeverityWarning, "The topology names a Builder file that publishing does not change."},
	{CodePublishLegacyReplaced, SeverityWarning, "This diagram replaced the topology's legacy Builder diagram."},
	{CodePublishLegacyRemoved, SeverityWarning, "The server could not read the topology's legacy Builder diagram. The server removed it."},
	{CodePublishExperimentUnrecorded, SeverityWarning, "The server stored the experiment, but not which draft published it."},
	{CodePublishExperimentPins, SeverityWarning, "The experiment update removed the host pins of VMs that are no longer in the topology."},
	{CodePublishScenarioPartial, SeverityWarning, "The server updated some scenarios before the scenario stage failed."},
	{CodePublishRetryComplete, SeverityWarning, "The same publication was already complete. The server wrote nothing."},
	{CodePublishRetryAvailable, SeverityWarning, "A stage failed after the server wrote other stages. " +
		"Publish again with the same choices to finish. The server skips the steps that are already done."},

	{CodePreflightCapacityCPU, SeverityWarning, "The schedulable hosts have fewer free CPUs than the devices take. " +
		"VMs would share CPUs."},
	{CodePreflightCapacityMemory, SeverityError, "The schedulable hosts have less free memory than the diagram's devices take."},
	{CodePreflightCapacityVMTooBig, SeverityError, "A device fits on no single schedulable host: none has both its CPUs and its memory."},
	{CodePreflightNetworkVLANRange, SeverityError, "The experiment's VLAN range is too small for the diagram, or leaves out a VLAN alias."},
	{CodePreflightNetworkAliasInUse, SeverityError, "A network's VLAN alias is a VLAN a running experiment already uses."},
	{CodePreflightNetworkBridge, SeverityWarning, "A bridge the diagram's interfaces or the experiment use does not exist on a host yet."},
	{CodePreflightDiskMissing, SeverityError, "A drive image is not one of the server's disk images."},
	{CodePreflightDiskKind, SeverityError, "A drive image is not of the kind its device needs: VM or ISO for kvm, container otherwise."},
	{CodePreflightAppMissing, SeverityError, "A scenario names an app that is not a default, built-in or user app of this server."},
	{CodePreflightScenarioMissing, SeverityError, "A scenario the diagram lists does not exist."},
	{CodePreflightScenarioUnread, SeverityWarning, "The caller may not read a scenario the diagram lists, " +
		"or the server cannot read it now."},
	{CodePreflightUnavailable, SeverityWarning, "The server could not do a preflight check, or part of one. The message says why."},

	{CodeDraftStale, SeverityError, "The If-Match entity tag names a version of the draft that is no longer current."},
	{CodeDraftConflict, SeverityError, "The draft changed while the server handled the request. Read the draft again and retry."},
	{CodeDraftSnapshotCurrent, SeverityError, "A request cannot delete the current version of a draft."},
	{CodeDraftLimit, SeverityError, "The server holds 2000 drafts, the most it may. Delete some, or run Clean up in the settings."},

	{CodeImportNameInvalid, SeverityError, "The new topology name of a copy or a combined import is not a config name."},
	{CodeImportNameSource, SeverityError, "The new topology name of a copy or a combined import is the imported topology's own name."},

	{CodeRequestInvalid, SeverityError, "The body, a header or a parameter of the request is not valid."},
	{CodeRequestForbidden, SeverityError, "The caller's role does not allow the operation."},
	{CodeRequestNotFound, SeverityError, "What the request names does not exist, or the caller may not see it."},
	{CodeRequestMethodNotAllowed, SeverityError, "The route does not take the method of the request. " +
		"The Allow header names the methods it takes."},
	{CodeRequestConflict, SeverityError, "The request conflicts with what is stored."},
	{CodeRequestStale, SeverityError, "The If-Match entity tag names a version that is no longer current."},
	{CodeRequestTooLarge, SeverityError, "The request or what it stores is larger than a limit."},
	{CodeRequestUnprocessable, SeverityError, "The request is well formed, but the server cannot do what it asks for."},
	{CodeServerError, SeverityError, "The server failed. Its log says why."},
	{CodeServerBusy, SeverityError, "The server stopped trying because what the request asked for kept changing. Try again."},
}

// codeIndex is the position of each code in [codeRegistry].
var codeIndex = func() map[Code]int { //nolint:gochecknoglobals // built once from the registry
	index := make(map[Code]int, len(codeRegistry))

	for i, info := range codeRegistry {
		index[info.Code] = i
	}

	return index
}()

// LookupCode returns what the registry says of code, and whether it holds
// it.
func LookupCode(code Code) (CodeInfo, bool) {
	i, ok := codeIndex[code]
	if !ok {
		return CodeInfo{Code: code, Severity: SeverityError, Description: ""}, false
	}

	return codeRegistry[i], true
}

// DefaultSeverity is the severity an issue with the code has unless the
// place that reports it says otherwise: an error for a code the registry
// does not hold.
func (c Code) DefaultSeverity() Severity {
	info, _ := LookupCode(c)

	return info.Severity
}

// NewIssue returns the issue of code at path, of the code's default
// severity, saying message. Its location by ID is left to the caller (see
// [Document.LocateIssues]).
func NewIssue(code Code, path, message string) Issue {
	return Issue{
		Code: code, Severity: code.DefaultSeverity(), Message: message, Path: path,
		NodeID: "", EdgeID: "", NetworkID: "", Field: "",
	}
}

// IssueMessages returns what each issue says, in order.
func IssueMessages(issues []Issue) []string {
	messages := make([]string, len(issues))

	for i, issue := range issues {
		messages[i] = issue.Message
	}

	return messages
}

// issueLister is an error that lists what it found as issues, as each
// publish blocker does.
type issueLister interface {
	Issues() []Issue
}

// ErrorIssues returns the issues err holds: those of a *[PackageError], a
// *[ValidationError], a *[TemplateFileError] or a *[LayerSetError], or what a publish blocker or
// another error with an Issues method lists (see [InterfaceVLANError]),
// anywhere in its chain. It returns nil for an error that holds none.
func ErrorIssues(err error) []Issue {
	var (
		pkg     *PackageError
		invalid *ValidationError
		file    *TemplateFileError
		set     *LayerSetError
		lister  issueLister
	)

	switch {
	case errors.As(err, &pkg):
		return slices.Clone(pkg.Issues)
	case errors.As(err, &invalid):
		return slices.Clone(invalid.Issues)
	case errors.As(err, &file):
		return slices.Clone(file.Issues)
	case errors.As(err, &set):
		return slices.Clone(set.Issues)
	case errors.As(err, &lister):
		return lister.Issues()
	}

	return nil
}
