package builder

import (
	"encoding"
	"encoding/json"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// The messages of the issues for diagram notes that are not a list and for a
// note that is not text.
const (
	notesNotList = "notes must be a list of text"
	noteNotText  = "note must be text"
)

// hideDescriptionMessage is the message of a hideDescription that is not true or false.
const hideDescriptionMessage = "hideDescription must be true or false"

// decodeProblem is one thing in the JSON of a document that the strict
// decoder refuses: a key its object does not have, or a value of the wrong
// type.
type decodeProblem struct {
	// path is where the problem is, as an issue names it: an unknown key's
	// own path (metadata.author), or the path of the value
	// (nodes[4].line.startArrow).
	path string
	// shape is the path with every list index written as [] and every key
	// of a map as * (nodes[].line.startArrow), which a rule is found by.
	shape string
	// key is the last part of the path: the unknown key, the field, or the
	// key of a map.
	key string
	// unknown is set for a key its object does not have.
	unknown bool
}

// The interfaces through which a type decodes itself. Such a type decides
// what it accepts.
//
//nolint:gochecknoglobals // two reflected interface types, read only
var (
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// decodeWalker collects the problems of a JSON value with the Go type it
// decodes into, as encoding/json refuses them when unknown fields are
// disallowed: a key no field of a struct is named (ignoring case, as the
// decoder matches keys), and a value of a type the field cannot hold.
type decodeWalker struct {
	problems []decodeProblem
}

// walk checks value, a JSON value as [json.Unmarshal] decodes it into an
// any, against target, at path.
func (w *decodeWalker) walk(target reflect.Type, value any, path, shape, key string) {
	// A null decodes into a value of any type, which it leaves as it is.
	if value == nil {
		return
	}

	for target.Kind() == reflect.Pointer && !decodesItself(target) {
		target = target.Elem()
	}

	if decodesItself(target) || target.Kind() == reflect.Interface {
		return
	}

	if !fitsType(target, value) {
		w.problems = append(w.problems, decodeProblem{path: path, shape: shape, key: key, unknown: false})

		return
	}

	switch typed := value.(type) {
	case map[string]any:
		w.walkObject(target, typed, path, shape)
	case []any:
		for i, item := range typed {
			w.walk(target.Elem(), item, path+"["+strconv.Itoa(i)+"]", shape+"[]", key)
		}
	}
}

// walkObject checks the keys and values of a JSON object against target, a
// struct or a map, at path.
func (w *decodeWalker) walkObject(target reflect.Type, object map[string]any, path, shape string) {
	keys := slices.Sorted(maps.Keys(object))

	if target.Kind() == reflect.Map {
		for _, key := range keys {
			w.walk(target.Elem(), object[key], joinDecodePath(path, key), joinDecodePath(shape, "*"), key)
		}

		return
	}

	fields := jsonFields(target)

	for _, key := range keys {
		field, found := jsonFieldOf(fields, key)
		if !found {
			w.problems = append(w.problems, decodeProblem{
				path: joinDecodePath(path, key), shape: joinDecodePath(shape, key), key: key, unknown: true,
			})

			continue
		}

		w.walk(field.typ, object[key], joinDecodePath(path, field.name), joinDecodePath(shape, field.name), field.name)
	}
}

// decodesItself reports whether values of target, or of a pointer to it,
// decode themselves.
func decodesItself(target reflect.Type) bool {
	pointer := reflect.PointerTo(target)

	return target.Implements(jsonUnmarshalerType) || pointer.Implements(jsonUnmarshalerType) ||
		target.Implements(textUnmarshalerType) || pointer.Implements(textUnmarshalerType)
}

// fitsType reports whether encoding/json decodes value into a Go value of
// type target without refusing its type. It does not check the contents of
// an object or a list.
func fitsType(target reflect.Type, value any) bool {
	kind := target.Kind()

	switch typed := value.(type) {
	case map[string]any:
		return kind == reflect.Struct || kind == reflect.Map
	case []any:
		return kind == reflect.Slice || kind == reflect.Array
	case string:
		// A list of bytes is decoded from base64 text.
		return kind == reflect.String || (kind == reflect.Slice && target.Elem().Kind() == reflect.Uint8)
	case bool:
		return kind == reflect.Bool
	case float64:
		return fitsNumber(kind, typed)
	}

	return false
}

// fitsNumber reports whether a JSON number decodes into a Go value of kind:
// any number into a float, a whole number into an integer, and a whole
// number that is not negative into an unsigned integer.
func fitsNumber(kind reflect.Kind, number float64) bool {
	whole := number == math.Trunc(number)

	switch {
	case kind == reflect.Float32 || kind == reflect.Float64:
		return true
	case kind >= reflect.Int && kind <= reflect.Int64:
		return whole
	case kind >= reflect.Uint && kind <= reflect.Uintptr:
		return whole && number >= 0
	}

	return false
}

// jsonField is a field of a struct as encoding/json names it.
type jsonField struct {
	name string
	typ  reflect.Type
}

// jsonFields returns the fields through which encoding/json decodes a JSON
// object into a value of the struct type target. Each field has the name of
// its json tag, else its own name. It leaves out a field tagged "-" and an
// unexported field, and it includes the fields of an embedded struct.
func jsonFields(target reflect.Type) []jsonField {
	fields := make([]jsonField, 0, target.NumField())

	for i := range target.NumField() {
		field := target.Field(i)
		tag := field.Tag.Get("json")

		if tag == "-" {
			continue
		}

		name, _, _ := strings.Cut(tag, ",")

		if embedded := field.Type; field.Anonymous && name == "" {
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}

			if embedded.Kind() == reflect.Struct {
				fields = append(fields, jsonFields(embedded)...)

				continue
			}
		}

		if !field.IsExported() {
			continue
		}

		if name == "" {
			name = field.Name
		}

		fields = append(fields, jsonField{name: name, typ: field.Type})
	}

	return fields
}

// jsonFieldOf returns the field a key of a JSON object decodes into: the
// field of that name, else one whose name is the key in other case, as
// encoding/json matches keys.
func jsonFieldOf(fields []jsonField, key string) (jsonField, bool) {
	if i := slices.IndexFunc(fields, func(field jsonField) bool { return field.name == key }); i >= 0 {
		return fields[i], true
	}

	if i := slices.IndexFunc(fields, func(field jsonField) bool { return strings.EqualFold(field.name, key) }); i >= 0 {
		return fields[i], true
	}

	return jsonField{}, false
}

// joinDecodePath is the path of key in the object at path: the key alone at
// the root.
func joinDecodePath(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

// decodeIssues returns an issue for each unknown key, and for each value of
// the wrong type at a path the editor checks (see [decodeRule]), in a document
// that [Decode] refused. Else it returns nil, and the decoder error tells why.
func decodeIssues(data []byte) []Issue {
	var value any

	if err := json.Unmarshal(data, &value); err != nil {
		return nil
	}

	var walker decodeWalker

	walker.walk(reflect.TypeFor[Document](), value, "", "", "")

	if len(walker.problems) == 0 {
		return nil
	}

	issues := make([]Issue, 0, len(walker.problems))

	for _, problem := range walker.problems {
		if problem.unknown {
			issues = append(issues, NewIssue(CodeDocumentFieldUnknown, problem.path, `unknown field "`+problem.key+`"`))

			continue
		}

		code, message := decodeRule(problem)
		if code == "" {
			return nil
		}

		issues = append(issues, NewIssue(code, problem.path, message))
	}

	slices.SortStableFunc(issues, func(a, b Issue) int { return strings.Compare(a.Path, b.Path) })

	return issues
}

// decodeRule is the code and the message of a value of the wrong type at the
// paths whose type the editor checks, in its words (decode.js, validate.js),
// or no code for a value at any other path.
func decodeRule(problem decodeProblem) (Code, string) {
	switch problem.shape {
	case keyMetadata:
		return CodeMetadataNotObject, "metadata must be an object"
	case "metadata.createdBy", "metadata.updatedBy":
		return CodeMetadataUserNotText, problem.path + " must be a string"
	case "metadata.createdAt", "metadata.updatedAt":
		return CodeMetadataTimeNotText, problem.path + " must be a string"
	case "metadata.notes":
		return CodeMetadataNotesNotList, notesNotList
	case "metadata.notes[]":
		return CodeMetadataNoteNotText, noteNotText
	case "nodes[].device.layer":
		return CodeDeviceLayerRange, layerRangeMessage
	case "nodes[].switch.layer":
		return CodeSwitchLayerRange, layerRangeMessage
	case "templates[].device.layer":
		return CodeTemplateLayerRange, layerRangeMessage
	case "layers[].id":
		return CodeLayerIDRange, layerIDRangeMessage
	case "networks[].subnet":
		return CodeNetworkSubnetInvalid, "subnet must be a string"
	case "networks[].gateway":
		return CodeNetworkGatewayInvalid, "gateway must be a string"
	case "nodes[].line.startArrow", "nodes[].line.endArrow":
		return CodeDrawingArrowNotBoolean, problem.key + " must be true or false"
	case "nodes[].device.hideDescription":
		return CodeDeviceHideDescriptionType, hideDescriptionMessage
	case "nodes[].group.hideDescription":
		return CodeGroupHideDescriptionType, hideDescriptionMessage
	case "templates[].device.hideDescription":
		return CodeTemplateHideDescriptionType, hideDescriptionMessage
	case keyNodes, keyNetworks, keyEdges:
		return CodeDocumentListMissing, problem.path + " must be an array"
	case keyLayout:
		return CodeDocumentLayoutNotText, "layout must be a string"
	case keyTemplates:
		return CodeTemplateListNotList, "templates must be a list of templates"
	case keyIcons:
		return CodeIconListNotObject, "custom icons must be an object of icons by name"
	case "source.includeTopologies", "source.unresolvedIncludes":
		return CodeIncludeListNotList, "included topologies must be a list of topology names"
	case "source.includeTopologies[]", "source.unresolvedIncludes[]":
		return CodeIncludeNameRequired, "an included topology needs a name"
	case "source.annotations":
		return CodeSourceAnnotationsNotMap, "annotations must be an object of text values"
	case "source.annotations.*":
		return CodeSourceAnnotationNotText, `annotation "` + problem.key + `" must be text`
	}

	return layerDecodeRule(problem)
}

// layerDecodeRule is [decodeRule] for the node matchers of a layer and the
// layer set options of the document.
func layerDecodeRule(problem decodeProblem) (Code, string) {
	switch problem.shape {
	case "layers[].matchers":
		return CodeLayerMatchersNotObject, "node matchers must be an object of patterns by field"
	case "layers[].matchers.*":
		return CodeLayerMatcherPatternNotText, `node matcher pattern of "` + problem.key + `" must be text`
	case keyLayerSet:
		return CodeLayerSetNotObject, "layer set options must be an object"
	case "layerSet.layerOutlines":
		return CodeLayerSetOutlinesNotBoolean, "layerOutlines must be true or false"
	case "layerSet.source.version":
		return CodeLayerSetSourceVersionInvalid, layerSetVersionMessage
	}

	return "", ""
}
