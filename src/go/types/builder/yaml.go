package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnsupportedYAML is returned, wrapped, by [JSONFromYAML] for text that is
// not YAML, or that uses YAML a Builder document may not: an alias or an
// anchor, a merge key, a second document, a key that is not a scalar, a key
// used twice, a tag other than the ones JSON has a value for, or a number
// JSON cannot hold.
var ErrUnsupportedYAML = errors.New("unsupported YAML")

// The short forms of the YAML tags a Builder document may hold.
const (
	yamlTagNull  = "!!null"
	yamlTagBool  = "!!bool"
	yamlTagInt   = "!!int"
	yamlTagFloat = "!!float"
	yamlTagStr   = "!!str"
	yamlTagMap   = "!!map"
	yamlTagSeq   = "!!seq"
	yamlTagMerge = "!!merge"
)

// yamlQuoted is set on a scalar written quoted or as a block, which is
// always text.
const yamlQuoted = yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle | yaml.LiteralStyle | yaml.FoldedStyle

var (
	// yamlFloat matches the plain scalars the browser's YAML reader takes for
	// a floating point number, and yamlFloatSpecial those of them JSON has no
	// value for: the infinities and "not a number".
	yamlFloat = regexp.MustCompile(
		`^(?:[-+]?[0-9]+(?:\.[0-9]*)?(?:[eE][-+]?[0-9]+)?` +
			`|\.[0-9]+(?:[eE][-+]?[0-9]+)?` +
			`|[-+]?\.(?:inf|Inf|INF)` +
			`|\.(?:nan|NaN|NAN))$`,
	)
	yamlFloatSpecial = regexp.MustCompile(`^(?:[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
)

// JSONFromText returns the JSON of the text of a Builder file, which holds a
// document as JSON or as YAML. The content decides, never the name of the
// file, as when a file is uploaded in the Builder: text that is JSON is
// returned as it is, and anything else is converted by [JSONFromYAML]. The
// result is not checked to be a Builder document: [Decode] and [Parse] do
// that.
func JSONFromText(data []byte) ([]byte, error) {
	if json.Valid(data) {
		return data, nil
	}

	return JSONFromYAML(data)
}

// documentSchemaPrefix starts the schema URI of every revision of the
// Builder document schema (see [SchemaURI]).
const documentSchemaPrefix = "https://phenix.sandia.gov/schemas/builder/"

// templateFileSchemaPrefix starts the schema URI of every revision of the
// template file format (see [TemplateFileSchemaURI]), which is below
// [documentSchemaPrefix] but names no Builder document.
const templateFileSchemaPrefix = documentSchemaPrefix + "templates/"

// IsDocumentText reports whether text, the content of a JSON or YAML file,
// is a Builder document and not a phenix config: a map whose "$schema" is a
// Builder document schema URI, of any revision, and that has no "kind". The
// two share no key, so the content tells them apart where the name of the
// file does not: the Builder exports a document as plain .json or .yaml. A
// template file (see [TemplateFileSchemaURI]) is not a Builder document.
//
// Only those two keys are looked at, and YAML is read leniently, so a
// document that is not valid, or that uses YAML [JSONFromYAML] refuses, is
// still recognized as one.
func IsDocumentText(text []byte) bool {
	schema, kind, ok := textHead(text)

	return ok && !kind && strings.HasPrefix(schema, documentSchemaPrefix) &&
		!strings.HasPrefix(schema, templateFileSchemaPrefix)
}

// textHead returns the "$schema" of text, the content of a JSON or YAML
// file, and whether it has a "kind". The last result is false for text that
// is not a map, or whose "$schema" is not text.
func textHead(text []byte) (string, bool, bool) {
	if json.Valid(text) {
		var (
			keys   map[string]json.RawMessage
			schema string
		)

		if json.Unmarshal(text, &keys) != nil || json.Unmarshal(keys["$schema"], &schema) != nil {
			return "", false, false
		}

		_, kind := keys["kind"]

		return schema, kind, true
	}

	var head struct {
		Schema string    `yaml:"$schema"`
		Kind   yaml.Node `yaml:"kind"`
	}

	if yaml.Unmarshal(text, &head) != nil {
		return "", false, false
	}

	return head.Schema, !head.Kind.IsZero(), true
}

// JSONFromYAML converts the YAML text of a Builder document to JSON, reading
// it as the Builder does when a YAML file is uploaded, so that a document has
// the same content, and the same digest, whichever of the two reads it.
//
// A scalar that is quoted, or written as a block, is text. A plain scalar is
// null (empty, "~", "null"), a boolean ("true", "false"), a number, or else
// text: "yes", "on" and a date such as 2001-01-01 stay text, which is what
// the Builder writes them as when it exports a document as YAML. A number is
// a decimal integer (012 is twelve), an integer with a 0b, 0o or 0x prefix, or
// a decimal fraction with an optional exponent. A key is the text of its
// scalar: the key 1 is "1".
//
// Text using YAML a JSON document has no place for is refused, with an error
// matching [ErrUnsupportedYAML] that says where: see that error for the
// list. The Builder refuses aliases too; anchors and merge keys are refused
// because they have no use without one.
func JSONFromYAML(data []byte) ([]byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var root yaml.Node

	if err := decoder.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: the text holds no document", ErrUnsupportedYAML)
		}

		return nil, fmt.Errorf("%w: %w", ErrUnsupportedYAML, err)
	}

	var second yaml.Node

	switch err := decoder.Decode(&second); {
	case errors.Is(err, io.EOF):
	case err == nil:
		return nil, yamlRefusal(&second, "a second document")
	default:
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedYAML, err)
	}

	value, err := yamlValue(&root)
	if err != nil {
		return nil, err
	}

	var encoded bytes.Buffer

	// As JSON.stringify writes it: "<" stays "<".
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupportedYAML, err)
	}

	return bytes.TrimSuffix(encoded.Bytes(), []byte("\n")), nil
}

// yamlRefusal is the error for a node holding what a Builder document may
// not. It names the line and what was found, never the node's content.
func yamlRefusal(node *yaml.Node, found string) error {
	return fmt.Errorf("%w: line %d: %s is not supported in a Builder document", ErrUnsupportedYAML, node.Line, found)
}

// yamlValue returns the JSON value of a YAML node: a map, a list, or the
// value of a scalar (see [yamlScalar]).
func yamlValue(node *yaml.Node) (any, error) {
	switch {
	case node.Kind == yaml.AliasNode:
		return nil, yamlRefusal(node, "an alias (*name)")
	case node.Anchor != "":
		return nil, yamlRefusal(node, "an anchor (&name)")
	}

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return nil, yamlRefusal(node, "a document without one value")
		}

		return yamlValue(node.Content[0])
	case yaml.MappingNode:
		if node.Style&yaml.TaggedStyle != 0 && node.Tag != yamlTagMap {
			return nil, yamlRefusal(node, "the tag of this map")
		}

		return yamlMap(node)
	case yaml.SequenceNode:
		if node.Style&yaml.TaggedStyle != 0 && node.Tag != yamlTagSeq {
			return nil, yamlRefusal(node, "the tag of this list")
		}

		values := make([]any, 0, len(node.Content))

		for _, item := range node.Content {
			value, err := yamlValue(item)
			if err != nil {
				return nil, err
			}

			values = append(values, value)
		}

		return values, nil
	case yaml.ScalarNode:
		return yamlScalar(node)
	case yaml.AliasNode:
	}

	return nil, yamlRefusal(node, "this value")
}

// yamlMap returns the JSON object of a YAML map. Its keys are scalars, each
// used once.
func yamlMap(node *yaml.Node) (map[string]any, error) {
	const pair = 2

	values := make(map[string]any, len(node.Content)/pair)

	for i := 0; i+1 < len(node.Content); i += pair {
		key, err := yamlKey(node.Content[i])
		if err != nil {
			return nil, err
		}

		if _, used := values[key]; used {
			return nil, yamlRefusal(node.Content[i], "a key used twice in one map")
		}

		value, err := yamlValue(node.Content[i+1])
		if err != nil {
			return nil, err
		}

		values[key] = value
	}

	return values, nil
}

// yamlKey returns the text of a map key. A key that is not text is written
// as JSON writes its value, which is how the Builder names it: the key 1.0
// is "1" and the key ~ is "null".
func yamlKey(node *yaml.Node) (string, error) {
	switch {
	case node.Kind == yaml.AliasNode:
		return "", yamlRefusal(node, "an alias (*name)")
	case node.Anchor != "":
		return "", yamlRefusal(node, "an anchor (&name)")
	case node.Kind != yaml.ScalarNode:
		return "", yamlRefusal(node, "a key that is a map or a list")
	case node.Tag == yamlTagMerge:
		return "", yamlRefusal(node, "a merge key (<<)")
	}

	value, err := yamlScalar(node)
	if err != nil {
		return "", err
	}

	if text, ok := value.(string); ok {
		return text, nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return "", yamlRefusal(node, "this key")
	}

	return string(encoded), nil
}

// yamlScalar returns the JSON value of a scalar: nil, a bool, a float64 or a
// string. Its text, with how it is written, decides (see [JSONFromYAML]);
// the tag yaml.v3 resolved for a plain scalar does not, since it takes a date
// for a time and 012 for an octal number.
func yamlScalar(node *yaml.Node) (any, error) {
	text := node.Value
	plain := node.Style&yamlQuoted == 0

	if node.Style&yaml.TaggedStyle != 0 {
		var (
			value any
			ok    bool
		)

		switch node.Tag {
		case yamlTagStr:
			return text, nil
		case yamlTagNull:
			ok = yamlNull(text) && (plain || text != "")
		case yamlTagBool:
			value, ok = yamlBool(text)
		case yamlTagInt:
			value, ok = yamlInt(text)
		case yamlTagFloat:
			if yamlFloatSpecial.MatchString(text) {
				return nil, yamlRefusal(node, "an infinite number or one that is not a number")
			}

			value, ok = yamlFloatValue(text)
		}

		if !ok {
			return nil, yamlRefusal(node, "the tag of this value")
		}

		return value, nil
	}

	if !plain {
		return text, nil
	}

	if yamlNull(text) {
		return nil, nil //nolint:nilnil // null is the value
	}

	if value, ok := yamlBool(text); ok {
		return value, nil
	}

	if value, ok := yamlInt(text); ok {
		return value, nil
	}

	if yamlFloatSpecial.MatchString(text) {
		return nil, yamlRefusal(node, "an infinite number or one that is not a number")
	}

	if value, ok := yamlFloatValue(text); ok {
		return value, nil
	}

	return text, nil
}

// yamlNull reports whether a plain scalar is null.
func yamlNull(text string) bool {
	switch text {
	case "", "~", "null", "Null", "NULL":
		return true
	}

	return false
}

// yamlBool returns the boolean a plain scalar is, if it is one.
func yamlBool(text string) (bool, bool) {
	switch text {
	case "true", "True", "TRUE":
		return true, true
	case "false", "False", "FALSE":
		return false, true
	}

	return false, false
}

// yamlInt returns the integer a plain scalar is, if it is one: decimal
// digits, or binary, octal or hexadecimal digits after 0b, 0o or 0x, with an
// optional sign. It is a float64 because the Builder holds every number as
// one, so both read a long integer as the same value. One too large to be a
// finite float64 is not a number at all.
func yamlInt(text string) (float64, bool) {
	const (
		binary      = 2
		octal       = 8
		decimal     = 10
		hexadecimal = 16

		// maxDigits is the most digits, after any leading zeros, an integer
		// a float64 holds can have: one of more digits is at least 2^1024
		// in every base, which no finite float64 is.
		maxDigits = 1024
	)

	digits := strings.TrimPrefix(text, "+")
	negative := strings.HasPrefix(text, "-")

	if negative {
		digits = text[1:]
	}

	base := decimal

	if len(digits) > 1 && digits[0] == '0' {
		switch digits[1] {
		case 'b':
			base, digits = binary, digits[2:]
		case 'o':
			base, digits = octal, digits[2:]
		case 'x':
			base, digits = hexadecimal, digits[2:]
		}
	}

	if digits == "" || strings.ContainsFunc(digits, func(r rune) bool { return !yamlDigit(r, base) }) {
		return 0, false
	}

	// Told by their number, not by parsing them: that takes time that grows
	// with the square of the number of digits, and a scalar may be as long
	// as a file.
	if len(strings.TrimLeft(digits, "0")) > maxDigits {
		return 0, false
	}

	parsed, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return 0, false
	}

	value, _ := new(big.Float).SetInt(parsed).Float64()
	if math.IsInf(value, 0) {
		return 0, false
	}

	// Never negative zero, which JSON would write as "-0".
	if negative && value != 0 {
		value = -value
	}

	return value, true
}

// yamlDigit reports whether r is a digit of the base.
func yamlDigit(r rune, base int) bool {
	const decimal = 10

	switch {
	case r >= '0' && r <= '9':
		return int(r-'0') < base
	case r >= 'a' && r <= 'f':
		return int(r-'a')+decimal < base
	case r >= 'A' && r <= 'F':
		return int(r-'A')+decimal < base
	}

	return false
}

// yamlFloatValue returns the floating point number a plain scalar is, if it
// is a finite one.
func yamlFloatValue(text string) (float64, bool) {
	if !yamlFloat.MatchString(text) {
		return 0, false
	}

	value, err := strconv.ParseFloat(text, 64)
	if (err != nil && !errors.Is(err, strconv.ErrRange)) || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}

	// Never negative zero, which JSON would write as "-0".
	if value == 0 {
		value = 0
	}

	return value, true
}
