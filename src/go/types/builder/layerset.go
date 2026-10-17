package builder

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Bounds on the node matchers of a layer (see [Layer.Matchers]). The editor
// checks the same bounds before it saves.
const (
	// MaxLayerMatchers is the most matchers one layer may have.
	MaxLayerMatchers = 16

	// MaxMatcherPathBytes bounds the field path of a matcher.
	MaxMatcherPathBytes = 128

	// MaxMatcherPatternBytes bounds the regular expression of a matcher.
	MaxMatcherPatternBytes = 256
)

// Bounds on a layer set (see [LayerSet]) and on the options a document
// keeps of one (see [DocumentLayerSet]).
const (
	// MaxLayerSetNameBytes bounds the name of a layer set.
	MaxLayerSetNameBytes = 128

	// MaxLayerSetDescriptionBytes bounds the description of a layer set.
	MaxLayerSetDescriptionBytes = 1024

	// MaxLayerSetIDBytes bounds the ID of a layer set that a document names
	// as its source.
	MaxLayerSetIDBytes = 128
)

// The rules that decide the layer of a node whose fields match the
// matchers of more than one layer (see [LayerSet.MatchConflict]). Empty is
// [MatchConflictLowest].
const (
	MatchConflictLowest  = "lowest"
	MatchConflictHighest = "highest"
)

// BuiltinPurdueLayerSetID is the ID of the built-in layer set of the Purdue
// model. Its ID is not one the server gives a set it stores, so the two
// never clash.
const BuiltinPurdueLayerSetID = "builtin:purdue"

// matcherPathPattern matches the field path of a matcher: dotted keys of
// the node spec, where a key of digits indexes a list.
const matcherPathPattern = `^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`

// layerSetIDPattern matches the ID of a layer set that a document names as
// its source: an ID the server gives a stored set, or the ID of a built-in
// set.
const layerSetIDPattern = `^[A-Za-z0-9_.:-]+$`

var (
	matcherPath    = regexp.MustCompile(matcherPathPattern)
	layerSetID     = regexp.MustCompile(layerSetIDPattern)
	matchConflicts = []string{MatchConflictLowest, MatchConflictHighest} //nolint:gochecknoglobals // immutable list
)

// IsLayerSetID reports whether id has the form of the ID of a layer set: 1
// to [MaxLayerSetIDBytes] letters, digits, ".", "_", ":" or "-".
func IsLayerSetID(id string) bool {
	return id != "" && len(id) <= MaxLayerSetIDBytes && layerSetID.MatchString(id)
}

// DocumentLayerSet is what a document keeps of the layer set its layers
// were copied from: the options of the set, and the set and the version it
// is linked to. The layers themselves are [Document.Layers]. A document
// stands on its own: it holds a full copy, and an edit of its layers changes
// only that copy. It is presentation only and never written to a config.
type DocumentLayerSet struct {
	// Name is the name of the set the layers were copied from. Empty for
	// layers that no set gave.
	Name string `json:"name,omitempty"`
	// MatchConflict is [MatchConflictLowest] or [MatchConflictHighest].
	// Empty is [MatchConflictLowest].
	MatchConflict string `json:"matchConflict,omitempty"`
	// LayerOutlines draws the outline of a node in the color of its layer,
	// when the layer has a color and the node no outline color of its own.
	LayerOutlines bool `json:"layerOutlines,omitempty"`
	// Source names the layer set of the server, and the version of it, the
	// layers were last copied from or saved to. Nil when the layers are
	// linked to no set.
	Source *LayerSetSource `json:"source,omitempty"`
}

// LayerSetSource names a layer set of the server and one version of it.
type LayerSetSource struct {
	// ID is the ID of the set (see [IsLayerSetID]).
	ID string `json:"id"`
	// Version is the version of the set, 1 or more.
	Version int64 `json:"version"`
}

// LayerSet is a named list of layers, with the options that apply to them,
// that a diagram copies (see [DocumentLayerSet]). The server keeps a
// library of layer sets that every user of the Builder shares, and a file
// holds one (see [LayerSetFile]).
type LayerSet struct {
	// Name is not blank, is at most [MaxLayerSetNameBytes] and holds no
	// control characters.
	Name string `json:"name"`
	// Description is at most [MaxLayerSetDescriptionBytes] and holds no
	// control characters.
	Description string `json:"description,omitempty"`
	// IconKey and Icon are the icon of the set, as [Device.IconKey] and
	// [Device.Icon] are the icon of a device.
	IconKey string `json:"iconKey,omitempty"`
	Icon    string `json:"icon,omitempty"`
	// MatchConflict and LayerOutlines are as in [DocumentLayerSet].
	MatchConflict string `json:"matchConflict,omitempty"`
	LayerOutlines bool   `json:"layerOutlines,omitempty"`
	// Layers are 1 to [MaxLayers] layers, which follow the rules of
	// [Document.Layers].
	Layers []Layer `json:"layers"`
}

// Issues returns what makes the layer set unusable, with paths under path
// ("" for a set at the root of a file):
//
//   - a name that is blank, longer than [MaxLayerSetNameBytes] or holds
//     control characters,
//   - a description longer than [MaxLayerSetDescriptionBytes] or holding
//     control characters,
//   - an icon key outside the icon key registry (see [IsIconKey]), or a
//     custom icon that is not an icon name (see [IconNameProblem]),
//   - a match conflict rule other than empty, lowest or highest,
//   - no layer, and layers that [Document.Validate] refuses in a document.
//
// Each issue carries the code of the rule it breaks.
func (s *LayerSet) Issues(path string) []Issue {
	var issues []Issue

	addf := func(code Code, at, format string, args ...any) {
		issues = append(issues, NewIssue(code, joinIssuePath(path, at), fmt.Sprintf(format, args...)))
	}

	switch {
	case strings.TrimSpace(s.Name) == "":
		addf(CodeLayerSetNameRequired, keyName, "layer set name is required")
	case len(s.Name) > MaxLayerSetNameBytes:
		addf(CodeLayerSetNameTooLong, keyName, "layer set name must be at most %d bytes", MaxLayerSetNameBytes)
	case strings.ContainsFunc(s.Name, isControl):
		addf(CodeLayerSetNameControl, keyName, "layer set name must not contain control characters")
	}

	switch {
	case len(s.Description) > MaxLayerSetDescriptionBytes:
		addf(
			CodeLayerSetDescriptionTooLong, keyDescription,
			"layer set description must be at most %d bytes", MaxLayerSetDescriptionBytes,
		)
	case strings.ContainsFunc(s.Description, isControl):
		addf(CodeLayerSetDescriptionControl, keyDescription, "layer set description must not contain control characters")
	}

	if problem := iconKeyProblem(s.IconKey); problem != "" {
		addf(CodeLayerSetIconKeyUnknown, keyIconKey, "%s", problem)
	}

	if s.Icon != "" {
		if problem := IconNameProblem(s.Icon); problem != "" {
			addf(CodeLayerSetIconInvalid, keyIcon, "%s", problem)
		}
	}

	if problem := matchConflictProblem(s.MatchConflict); problem != "" {
		addf(CodeLayerSetMatchConflictUnknown, keyMatchConflict, "%s", problem)
	}

	if len(s.Layers) == 0 {
		addf(CodeLayerSetLayersEmpty, keyLayers, "a layer set holds at least one layer")
	}

	return append(issues, layerListIssues(s.Layers, joinIssuePath(path, keyLayers))...)
}

// joinIssuePath joins the path of a part to the path of what holds it, ""
// for the root.
func joinIssuePath(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

// matchConflictProblem says why rule is no match conflict rule, or returns
// "".
func matchConflictProblem(rule string) string {
	if rule == "" || slices.Contains(matchConflicts, rule) {
		return ""
	}

	return fmt.Sprintf(
		"unknown match conflict rule %q (expected one of %s)", truncate(rule), strings.Join(matchConflicts, ", "),
	)
}

// documentLayerSetIssues returns what makes the layer set options of a
// document unusable: a name the rules of [LayerSet.Issues] refuse (empty is
// allowed), an unknown match conflict rule, and a source whose ID is not a
// layer set ID or whose version is below 1.
func documentLayerSetIssues(options *DocumentLayerSet) []Issue {
	var issues []Issue

	addf := func(code Code, at, format string, args ...any) {
		issues = append(issues, NewIssue(code, keyLayerSet+"."+at, fmt.Sprintf(format, args...)))
	}

	switch {
	case len(options.Name) > MaxLayerSetNameBytes:
		addf(CodeLayerSetNameTooLong, keyName, "layer set name must be at most %d bytes", MaxLayerSetNameBytes)
	case strings.ContainsFunc(options.Name, isControl):
		addf(CodeLayerSetNameControl, keyName, "layer set name must not contain control characters")
	}

	if problem := matchConflictProblem(options.MatchConflict); problem != "" {
		addf(CodeLayerSetMatchConflictUnknown, keyMatchConflict, "%s", problem)
	}

	if source := options.Source; source != nil {
		if !IsLayerSetID(source.ID) {
			addf(
				CodeLayerSetSourceIDInvalid, keySource+"."+keyID,
				`layer set ID %q must be 1 to %d letters, digits, ".", "_", ":" or "-"`,
				truncate(source.ID), MaxLayerSetIDBytes,
			)
		}

		if source.Version < 1 {
			addf(CodeLayerSetSourceVersionInvalid, keySource+"."+keyVersion, "%s", layerSetVersionMessage)
		}
	}

	return issues
}

// layerSetVersionMessage is the message of a source version that is not a
// positive integer. The decoder refuses a version that is not an integer
// with the same message (see [decodeRule]).
const layerSetVersionMessage = "layer set version must be an integer of 1 or more"

// layerListIssues returns what makes a list of layers at path unusable: more
// than [MaxLayers], a layer whose ID is not from 0 to [MaxLayerID] or is used
// twice, and a layer that [layerIssues] refuses.
func layerListIssues(layers []Layer, path string) []Issue {
	var issues []Issue

	if len(layers) > MaxLayers {
		issues = append(issues, NewIssue(
			CodeLayerListTooMany, path, fmt.Sprintf("at most %d layers are allowed, not %d", MaxLayers, len(layers)),
		))
	}

	seen := map[int]int{}

	for i := range layers {
		layer := &layers[i]
		at := fmt.Sprintf("%s[%d]", path, i)

		if layer.ID == nil || !isLayerID(*layer.ID) {
			issues = append(issues, NewIssue(CodeLayerIDRange, at+"."+keyID, layerIDRangeMessage))
		} else if prev, ok := seen[*layer.ID]; ok {
			issues = append(issues, NewIssue(
				CodeLayerIDDuplicate, at+"."+keyID,
				fmt.Sprintf("duplicate layer ID %d (also %s[%d])", *layer.ID, path, prev),
			))
		} else {
			seen[*layer.ID] = i
		}

		issues = append(issues, layerIssues(layer, at)...)
	}

	return issues
}

// layerIssues returns what makes one layer at path unusable, apart from its
// ID: a label that is blank, longer than [MaxLayerLabelBytes] or holds
// control characters, an icon key outside the registry, a custom icon that
// is not an icon name, a color that is not "#rrggbb", and matchers that
// [matcherIssues] refuses.
func layerIssues(layer *Layer, path string) []Issue {
	var issues []Issue

	addf := func(code Code, at, format string, args ...any) {
		issues = append(issues, NewIssue(code, path+"."+at, fmt.Sprintf(format, args...)))
	}

	switch {
	case strings.TrimSpace(layer.Label) == "":
		addf(CodeLayerLabelRequired, keyLabel, "layer label is required")
	case len(layer.Label) > MaxLayerLabelBytes:
		addf(CodeLayerLabelTooLong, keyLabel, "layer label must be at most %d bytes", MaxLayerLabelBytes)
	case strings.ContainsFunc(layer.Label, isControl):
		addf(CodeLayerLabelControl, keyLabel, "layer label must not contain control characters")
	}

	if problem := iconKeyProblem(layer.IconKey); problem != "" {
		addf(CodeLayerIconKeyUnknown, keyIconKey, "%s", problem)
	}

	if layer.Icon != "" {
		if problem := IconNameProblem(layer.Icon); problem != "" {
			addf(CodeLayerIconInvalid, keyIcon, "%s", problem)
		}
	}

	if problem := colorProblem(layer.Color); problem != "" {
		addf(CodeLayerColorInvalid, keyColor, "%s", problem)
	}

	return append(issues, matcherIssues(layer.Matchers, path+"."+keyMatchers)...)
}

// matcherIssues returns what makes the matchers of a layer at path
// unusable: more than [MaxLayerMatchers], a field path that is longer than
// [MaxMatcherPathBytes] or is not dotted keys of letters, digits, "_" and
// "-", and a pattern that is empty, longer than [MaxMatcherPatternBytes] or
// does not compile. Each issue about one matcher has the path of its key.
//
// Go's regexp (RE2) decides here. The editor also refuses a pattern that
// JavaScript's RegExp cannot compile, such as one with (?i), because the
// editor applies the matchers. RE2 refuses what JavaScript alone accepts,
// such as a lookahead or a backreference. Thus a pattern that both accept
// is the one kind that works everywhere.
func matcherIssues(matchers map[string]string, path string) []Issue {
	var issues []Issue

	if len(matchers) > MaxLayerMatchers {
		issues = append(issues, NewIssue(
			CodeLayerMatchersTooMany, path,
			fmt.Sprintf("at most %d node matchers are allowed, not %d", MaxLayerMatchers, len(matchers)),
		))
	}

	for _, field := range slices.Sorted(maps.Keys(matchers)) {
		at := path + "." + field
		pattern := matchers[field]

		switch {
		case len(field) > MaxMatcherPathBytes:
			issues = append(issues, NewIssue(
				CodeLayerMatcherPathTooLong, at,
				fmt.Sprintf("node matcher field must be at most %d bytes", MaxMatcherPathBytes),
			))
		case !matcherPath.MatchString(field):
			issues = append(issues, NewIssue(
				CodeLayerMatcherPathInvalid, at,
				fmt.Sprintf(
					`node matcher field %q must be keys of letters, digits, "_" or "-", joined by "."`, truncate(field),
				),
			))
		}

		issues = append(issues, matcherPatternIssues(pattern, at)...)
	}

	return issues
}

// matcherPatternIssues returns what makes the pattern of one matcher at path
// unusable.
func matcherPatternIssues(pattern, path string) []Issue {
	switch {
	case pattern == "":
		return []Issue{NewIssue(CodeLayerMatcherPatternRequired, path, "node matcher pattern is required")}
	case len(pattern) > MaxMatcherPatternBytes:
		return []Issue{NewIssue(
			CodeLayerMatcherPatternTooLong, path,
			fmt.Sprintf("node matcher pattern must be at most %d bytes", MaxMatcherPatternBytes),
		)}
	}

	if _, err := regexp.Compile(pattern); err != nil {
		return []Issue{NewIssue(
			CodeLayerMatcherPatternInvalid, path,
			fmt.Sprintf("node matcher pattern %q is not a regular expression", truncate(pattern)),
		)}
	}

	return nil
}

// BuiltinLayerSet is a layer set every server has, with its ID. A user
// cannot change or delete it, but can copy it.
type BuiltinLayerSet struct {
	LayerSet

	ID string
}

// purdueLevel is a level of the Purdue model as its built-in layer shows
// it.
type purdueLevel struct {
	label, iconKey, color string
}

// BuiltinLayerSets returns the layer sets every server has, each call a
// copy of its own: the levels of the Purdue model.
//
// The levels of the model are numbers, and one of them is 3.5. A layer ID is
// an integer, so the set gives the levels the IDs 0 to 6 from the bottom up,
// and the label of each layer names its level. Each color is a mid-tone that
// a node outline or a swatch shows with a contrast of 3:1 or more on a white
// and on a dark background.
func BuiltinLayerSets() []BuiltinLayerSet {
	levels := []purdueLevel{
		{"L5 Enterprise", IconServer, "#2f6fbf"},
		{"L4 Business", "laptop", "#8b5cf6"},
		{"L3.5 Industrial DMZ", iconFirewall, "#b45309"},
		{"L3 Operations", "terminal", "#0f766e"},
		{"L2 Supervisory control", iconDesktop, "#15803d"},
		{"L1 Basic control", "plc", "#db2777"},
		{"L0 Process", "transmission", "#dc2626"},
	}

	layers := make([]Layer, 0, len(levels))

	for i, level := range levels {
		id := len(levels) - 1 - i
		layers = append(layers, Layer{
			ID: &id, Label: level.label, IconKey: level.iconKey, Icon: "", Color: level.color, Matchers: nil,
		})
	}

	return []BuiltinLayerSet{
		{
			ID: BuiltinPurdueLayerSetID,
			LayerSet: LayerSet{
				Name: "Purdue Model",
				Description: "The levels of the Purdue model for industrial control systems. A layer ID is an " +
					"integer, so level 3.5, the industrial DMZ, has ID 4. The levels above it have the next IDs.",
				IconKey:       "plc",
				Icon:          "",
				MatchConflict: MatchConflictLowest,
				LayerOutlines: false,
				Layers:        layers,
			},
		},
	}
}
