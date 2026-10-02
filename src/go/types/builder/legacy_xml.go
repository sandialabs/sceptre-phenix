package builder

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds on a legacy Builder diagram. The input is untrusted, so each is
// checked while the diagram is read, before anything is built from it.
const (
	// MaxLegacyBytes bounds the diagram's XML (5 MiB, as a Builder document).
	MaxLegacyBytes = 5 << 20

	// maxLegacyDepth is the most levels of elements the XML may nest.
	maxLegacyDepth = 32

	// maxLegacyCells is the most cells the diagram may hold.
	maxLegacyCells = 10000

	// maxLegacySettingsDepth is the most levels the JSON settings of a cell
	// (its schemaVars) may nest.
	maxLegacySettingsDepth = 64

	// maxLegacyCoordinate bounds every number of a cell's geometry.
	maxLegacyCoordinate = 1000000.0

	// maxLegacyVariables, maxLegacyVariableName and maxLegacyVariableValue
	// bound the experiment variables of a diagram that are used.
	maxLegacyVariables     = 100
	maxLegacyVariableName  = 64
	maxLegacyVariableValue = 1024

	// maxLegacyShown is the most bytes of a name from a diagram that a
	// message or a warning shows.
	maxLegacyShown = 64
)

// The element and attribute names of mxGraph XML that are read.
const (
	legacyElementModel    = "mxGraphModel"
	legacyElementRoot     = "root"
	legacyElementCell     = "mxCell"
	legacyElementGeometry = "mxGeometry"
	legacyGeometryRole    = "geometry"
	legacyFlagSet         = "1"
)

// LegacyReason says which check of [DecodeLegacy] refused a diagram.
type LegacyReason string

const (
	// LegacyNotDiagram is input that is not XML at all.
	LegacyNotDiagram LegacyReason = "not-diagram"
	// LegacyMalformed is XML that is not well formed.
	LegacyMalformed LegacyReason = "malformed"
	// LegacyDoctype is XML with a DOCTYPE or another declaration.
	LegacyDoctype LegacyReason = "doctype"
	// LegacyRoot is XML whose root element is not a diagram's.
	LegacyRoot LegacyReason = "root"
	// LegacyTooLarge is input beyond [MaxLegacyBytes].
	LegacyTooLarge LegacyReason = "too-large"
	// LegacyTooDeep is XML that nests elements too deep.
	LegacyTooDeep LegacyReason = "too-deep"
	// LegacyTooMany is a diagram with too many cells.
	LegacyTooMany LegacyReason = "too-many"
)

// ErrLegacy is what every [LegacyError] unwraps to.
var ErrLegacy = errors.New("invalid legacy Builder diagram")

// LegacyError is why [DecodeLegacy] refused a diagram. Its message is one of
// a closed set and is meant to be shown as it is: it holds a line number or
// the name of the root element, and never anything else of the input.
type LegacyError struct {
	Reason  LegacyReason
	Message string
}

func (e *LegacyError) Error() string {
	return e.Message
}

// Unwrap lets [errors.Is] match [ErrLegacy].
func (e *LegacyError) Unwrap() error {
	return ErrLegacy
}

func legacyRefusal(reason LegacyReason, format string, args ...any) *LegacyError {
	return &LegacyError{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// LegacyDiagram is a decoded diagram of the legacy Builder: the cells of its
// mxGraph model in document order, and the experiment variables and grid
// setting of the model. [FromLegacy] and [FromLegacyTopology] convert one.
type LegacyDiagram struct {
	cells []*legacyCell
	byID  map[string]int
	// variables are the diagram's experiment variables, by name.
	variables map[string]string
	// grid is the model's grid attribute; "0" turns the grid off.
	grid string
}

// legacyCell is one cell of the model: an mxCell, with the wrapper element
// that carries its label and settings when it has one.
type legacyCell struct {
	id     string
	label  string
	parent string
	source string
	target string
	style  string
	vertex bool
	edge   bool
	box    legacyBox
	// relative marks a geometry whose x and y are fractions of the parent.
	relative bool
	// hasSettings reports a schemaVars attribute; settings is its content,
	// or nil when it could not be read.
	hasSettings bool
	settings    map[string]any
}

// legacyBox is a rectangle in the coordinates of the legacy diagram.
type legacyBox struct {
	x, y, width, height float64
}

func (b legacyBox) center() (float64, float64) {
	return b.x + b.width/2, b.y + b.height/2
}

func (b legacyBox) contains(x, y float64) bool {
	return b.width > 0 && b.height > 0 && x >= b.x && x <= b.x+b.width && y >= b.y && y <= b.y+b.height
}

func (b legacyBox) area() float64 {
	return b.width * b.height
}

func newLegacyDiagram() *LegacyDiagram {
	return &LegacyDiagram{cells: nil, byID: map[string]int{}, variables: map[string]string{}, grid: ""}
}

// DecodeLegacy reads a diagram of the legacy Builder: mxGraph XML whose root
// element is mxGraphModel, or a bare root. Nothing else is read: no encoded
// or compressed form, and no wrapper element.
//
// The input is untrusted. It is refused with a [LegacyError] when it is
// larger than [MaxLegacyBytes], is not well-formed UTF-8 XML, holds a
// DOCTYPE or any other declaration (so no entity is ever defined or
// expanded), nests elements more than 32 levels deep, or holds more than
// 10000 cells. Reading stops at the end of the root element.
//
// Everything else is tolerated: a cell without an id is given one, a
// repeated id keeps the first cell, a number that cannot be read is 0, an
// unknown parent or a cycle of parents makes a cell a top-level one, and
// settings that cannot be read are reported when the diagram is converted.
// Empty input is an empty diagram.
func DecodeLegacy(content []byte) (*LegacyDiagram, error) {
	if len(content) > MaxLegacyBytes {
		return nil, legacyRefusal(LegacyTooLarge, "the legacy diagram is larger than %d bytes", MaxLegacyBytes)
	}

	content = bytes.Trim(bytes.TrimPrefix(content, []byte("\xef\xbb\xbf")), " \t\r\n")
	if len(content) == 0 {
		return newLegacyDiagram(), nil
	}

	if content[0] != '<' {
		return nil, legacyRefusal(
			LegacyNotDiagram,
			"this is not a legacy Builder diagram: expected mxGraph XML, "+
				"or a Topology config with the %s annotation", LegacyXMLAnnotation,
		)
	}

	decoder := xml.NewDecoder(bytes.NewReader(content))
	decoder.Strict = true
	decoder.Entity = nil
	// Only UTF-8 is read: another declared encoding is an error.
	decoder.CharsetReader = nil

	reader := &legacyReader{ //nolint:exhaustruct // the state starts empty
		diagram: newLegacyDiagram(),
	}

	for depth := 0; ; {
		token, err := decoder.Token()
		if err != nil {
			return nil, legacyMalformed(decoder, err)
		}

		switch element := token.(type) {
		case xml.Directive:
			return nil, legacyRefusal(
				LegacyDoctype,
				"the legacy diagram has a DOCTYPE or entity declaration, which is not accepted",
			)
		case xml.StartElement:
			depth++

			if depth > maxLegacyDepth {
				return nil, legacyRefusal(
					LegacyTooDeep, "the legacy diagram nests elements more than %d levels deep", maxLegacyDepth,
				)
			}

			if err := reader.start(element, depth); err != nil {
				return nil, err
			}
		case xml.EndElement:
			reader.end(depth)

			depth--
			if depth == 0 {
				return reader.diagram, nil
			}
		case xml.CharData, xml.Comment, xml.ProcInst:
		}
	}
}

// legacyMalformed is the refusal of XML the decoder could not read. It
// gives the line and nothing of the decoder's own message, which may quote
// the input.
func legacyMalformed(decoder *xml.Decoder, err error) *LegacyError {
	line, _ := decoder.InputPos()

	var syntax *xml.SyntaxError
	if errors.As(err, &syntax) {
		line = syntax.Line
	}

	return legacyRefusal(LegacyMalformed, "the legacy diagram is not well-formed XML (line %d)", line)
}

// legacyReader builds a diagram from the elements of mxGraph XML. Cells are
// the direct children of the root container: an mxCell, or a wrapper element
// of any name holding one. A cell's geometry is a direct child of its
// mxCell. Every other element is passed over.
type legacyReader struct {
	diagram *LegacyDiagram
	// container is the depth of the root container's element, and inside
	// reports that it is open.
	container int
	inside    bool
	closed    bool
	// cell is the cell being read, wrapped reports that it has a wrapper
	// element, and inner that the wrapper's mxCell is open.
	cell     *legacyCell
	wrapped  bool
	hasInner bool
	inner    bool
	hasBox   bool
	settings string
	count    int
}

func (r *legacyReader) start(element xml.StartElement, depth int) error {
	name := element.Name.Local

	switch {
	case depth == 1:
		return r.startDocument(element)
	case !r.inside:
		// The first root element of a model is its container.
		if depth == r.container && name == legacyElementRoot && !r.closed {
			r.inside = true
		}
	case depth == r.container+1:
		return r.startCell(element)
	case r.cell == nil:
	case depth == r.container+2 && r.wrapped && name == legacyElementCell && !r.hasInner:
		r.hasInner, r.inner = true, true
		r.readCellAttributes(element)
	case name == legacyElementGeometry && !r.hasBox &&
		((depth == r.container+2 && !r.wrapped) || (depth == r.container+3 && r.inner)):
		r.readGeometry(element)
	}

	return nil
}

// startDocument checks the document's root element: an mxGraphModel, whose
// root child holds the cells, or that root alone.
func (r *legacyReader) startDocument(element xml.StartElement) error {
	switch element.Name.Local {
	case legacyElementModel:
		r.container = 2

		for _, attr := range element.Attr {
			switch attr.Name.Local {
			case keyGrid:
				r.diagram.grid = attr.Value
			case "experimentVars":
				r.diagram.variables = decodeLegacyVariables(attr.Value)
			}
		}
	case legacyElementRoot:
		r.container, r.inside = 1, true
	default:
		return legacyRefusal(
			LegacyRoot,
			"the XML is not a legacy Builder diagram: its root element is <%s>, not <%s>",
			cutBytes(element.Name.Local, maxLegacyShown), legacyElementModel,
		)
	}

	return nil
}

func (r *legacyReader) startCell(element xml.StartElement) error {
	r.count++
	if r.count > maxLegacyCells {
		return legacyRefusal(LegacyTooMany, "the legacy diagram has more than %d cells", maxLegacyCells)
	}

	r.cell = &legacyCell{} //nolint:exhaustruct // filled from the attributes
	r.wrapped = element.Name.Local != legacyElementCell
	r.hasInner, r.inner, r.hasBox, r.settings = false, false, false, ""

	if !r.wrapped {
		r.readCellAttributes(element)

		return nil
	}

	for _, attr := range element.Attr {
		switch attr.Name.Local {
		case keyID:
			r.cell.id = attr.Value
		case "label":
			r.cell.label = attr.Value
		case "schemaVars":
			r.cell.hasSettings = true
			r.settings = attr.Value
		}
	}

	return nil
}

// readCellAttributes reads an mxCell. A wrapper's own id and label win over
// those of the mxCell inside it.
func (r *legacyReader) readCellAttributes(element xml.StartElement) {
	for _, attr := range element.Attr {
		switch attr.Name.Local {
		case keyID:
			if r.cell.id == "" {
				r.cell.id = attr.Value
			}
		case "value":
			if !r.wrapped {
				r.cell.label = attr.Value
			}
		case "parent":
			r.cell.parent = attr.Value
		case "source":
			r.cell.source = attr.Value
		case "target":
			r.cell.target = attr.Value
		case "style":
			r.cell.style = attr.Value
		case "vertex":
			r.cell.vertex = attr.Value == legacyFlagSet
		case "edge":
			r.cell.edge = attr.Value == legacyFlagSet
		}
	}
}

func (r *legacyReader) readGeometry(element xml.StartElement) {
	var (
		box      legacyBox
		relative bool
	)

	for _, attr := range element.Attr {
		switch attr.Name.Local {
		case "as":
			if attr.Value != legacyGeometryRole {
				return
			}
		case "x":
			box.x = legacyNumber(attr.Value)
		case "y":
			box.y = legacyNumber(attr.Value)
		case "width":
			box.width = legacyNumber(attr.Value)
		case "height":
			box.height = legacyNumber(attr.Value)
		case "relative":
			relative = attr.Value == legacyFlagSet
		}
	}

	r.hasBox, r.cell.box, r.cell.relative = true, box, relative
}

func (r *legacyReader) end(depth int) {
	switch {
	case !r.inside:
	case depth == r.container:
		r.inside, r.closed = false, true
	case depth == r.container+1:
		r.finishCell()
	case depth == r.container+2:
		r.inner = false
	}
}

// finishCell adds the cell that just closed to the diagram, unless a cell
// of the same id is already there.
func (r *legacyReader) finishCell() {
	cell := r.cell
	r.cell = nil

	if cell == nil {
		return
	}

	if cell.id == "" {
		cell.id = "#" + strconv.Itoa(r.count-1)
	}

	if _, repeated := r.diagram.byID[cell.id]; repeated {
		return
	}

	if cell.hasSettings {
		cell.settings = legacySettings(r.settings)
	}

	r.diagram.byID[cell.id] = len(r.diagram.cells)
	r.diagram.cells = append(r.diagram.cells, cell)
}

// legacyNumber reads a number of a cell's geometry. What is not a finite
// number is 0, and the rest is kept within maxLegacyCoordinate.
func legacyNumber(text string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}

	return math.Max(-maxLegacyCoordinate, math.Min(maxLegacyCoordinate, value))
}

// legacySettings decodes the JSON settings of a cell: an object that nests
// at most maxLegacySettingsDepth levels. It returns nil for anything else.
func legacySettings(text string) map[string]any {
	var decoded any

	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return nil
	}

	settings, ok := decoded.(map[string]any)
	if !ok || jsonDeeperThan(settings, maxLegacySettingsDepth) {
		return nil
	}

	normalized, err := normalizeSpecMap(settings)
	if err != nil {
		return nil
	}

	return normalized
}

// jsonDeeperThan reports whether a decoded JSON value nests objects and
// arrays more than limit levels deep. It never descends further than that.
func jsonDeeperThan(value any, limit int) bool {
	switch typed := value.(type) {
	case map[string]any:
		if limit == 0 {
			return true
		}

		for _, child := range typed {
			if jsonDeeperThan(child, limit-1) {
				return true
			}
		}
	case []any:
		if limit == 0 {
			return true
		}

		for _, child := range typed {
			if jsonDeeperThan(child, limit-1) {
				return true
			}
		}
	}

	return false
}

var legacyVariableName = regexp.MustCompile(`^[A-Z_0-9]+$`)

// decodeLegacyVariables reads the experiment variables of a model: a JSON list of
// {name, value}. An entry is used when its name holds capital letters,
// digits and underscores only and its value is text or a number, within
// the bounds above; the first entry of a name wins. Anything else is passed
// over.
func decodeLegacyVariables(text string) map[string]string {
	variables := map[string]string{}

	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()

	var entries []any

	if err := decoder.Decode(&entries); err != nil {
		return variables
	}

	for _, entry := range entries {
		if len(variables) == maxLegacyVariables {
			break
		}

		fields, _ := entry.(map[string]any)
		name, _ := fields[keyName].(string)

		var value string

		switch typed := fields["value"].(type) {
		case string:
			value = typed
		case json.Number:
			value = typed.String()
		default:
			continue
		}

		_, taken := variables[name]
		if taken || len(name) > maxLegacyVariableName || len(value) > maxLegacyVariableValue ||
			!legacyVariableName.MatchString(name) {
			continue
		}

		variables[name] = value
	}

	return variables
}

// legacyStyle is the style of a cell: its bare tokens, and its key=value
// pairs.
type legacyStyle struct {
	bare   []string
	values map[string]string
}

func parseLegacyStyle(style string) legacyStyle {
	parsed := legacyStyle{bare: nil, values: map[string]string{}}

	for token := range strings.SplitSeq(style, ";") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}

		if key, value, found := strings.Cut(token, "="); found {
			parsed.values[key] = value
		} else {
			parsed.bare = append(parsed.bare, token)
		}
	}

	return parsed
}

// rectangleLike reports a plain rectangle, which may be a container: the
// style names no shape, and its only bare tokens are those of mxGraph's own
// containers.
func (s legacyStyle) rectangleLike() bool {
	if _, shaped := s.values["shape"]; shaped {
		return false
	}

	for _, token := range s.bare {
		if token != "group" && token != "swimlane" {
			return false
		}
	}

	return true
}

// imageName is the file name of the style's image, in lower case. The image
// itself is never read.
func (s legacyStyle) imageName() string {
	image := s.values["image"]

	return strings.ToLower(image[strings.LastIndex(image, "/")+1:])
}

var (
	legacyLineBreak = regexp.MustCompile(`(?i)<br\s*/?>|</(?:div|p|li)\s*>`)
	legacyTag       = regexp.MustCompile(`<[^>]*>`)
	legacyBlankRun  = regexp.MustCompile(`\n{3,}`)
)

// text is the cell's label as plain text. A label of a cell whose style
// allows HTML loses its markup: line and block ends become line breaks,
// every other tag goes, and character references are resolved. Control
// characters go too, and runs of empty lines are folded into one.
func (c *legacyCell) text() string {
	label := c.label

	if parseLegacyStyle(c.style).values["html"] == legacyFlagSet {
		label = legacyLineBreak.ReplaceAllString(label, "\n")
		label = legacyTag.ReplaceAllString(label, "")
		label = html.UnescapeString(label)
	}

	label = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}

		return r
	}, label)

	return legacyBlankRun.ReplaceAllString(strings.TrimSpace(label), "\n\n")
}

// cutBytes shortens text to at most limit bytes of whole characters.
func cutBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}

	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}

	return text[:cut]
}
