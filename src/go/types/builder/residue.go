package builder

import (
	"reflect"
	"strings"
	"sync"

	v1 "phenix/types/version/v1"
)

// bridgeSetInTopoKey is the key under which phenix stores, on each interface
// of an experiment, whether the topology set its bridge (see
// v1.Network.SetDefaults).
const bridgeSetInTopoKey = "bridge_set_in_topo"

// fieldKind tells which values of a node spec field phenix reads as no value.
type fieldKind int

const (
	// fieldScalar is a string, number or bool. Its zero value is no value.
	fieldScalar fieldKind = iota
	// fieldPointer is a pointer to a scalar. Only null is no value.
	fieldPointer
	// fieldList is a list of scalars. An empty list is no value.
	fieldList
	// fieldMap is a map. An empty map is no value.
	fieldMap
	// fieldObject is a struct, or a pointer to one.
	fieldObject
	// fieldObjects is a list of structs. An empty list is no value.
	fieldObjects
)

// shapeField is a field of a phenix v1 node spec struct.
type shapeField struct {
	kind fieldKind
	// shape describes a fieldObject, or each item of a fieldObjects.
	shape *structShape
	// def is the value that Init gives the field when a node sets none.
	def any
}

// structShape is a phenix v1 node spec struct, by the keys phenix stores.
type structShape struct {
	typ    reflect.Type
	fields map[string]shapeField
}

// nodeShape is the shape of a v1.Node. Its defaults are those that Init gives
// a node that sets nothing. Init gets a blank default bridge, so it leaves the
// bridge of a blank interface alone: [dropBridgeDefaults] handles bridges.
var nodeShape = sync.OnceValue(func() *structShape { //nolint:gochecknoglobals // built once per process
	node := &v1.Node{ //nolint:exhaustruct // Init fills in the defaults
		GeneralF:  &v1.General{HostnameF: "defaults"}, //nolint:exhaustruct // Init fills in the defaults
		HardwareF: &v1.Hardware{},                     //nolint:exhaustruct // Init fills in the defaults
		NetworkF: &v1.Network{ //nolint:exhaustruct // Init fills in the defaults
			InterfacesF: []*v1.Interface{{}},
		},
	}

	// Init sets the defaults even when it refuses a node.
	_ = (&v1.TopologySpec{NodesF: []*v1.Node{node}}).Init("") //nolint:exhaustruct // only nodes are needed
	node.GeneralF.HostnameF = ""

	return shapeOf(reflect.TypeFor[v1.Node](), reflect.ValueOf(node).Elem())
})

// shapeOf describes struct type typ. sample, when valid, is a value of typ
// after Init, which gives the defaults.
func shapeOf(typ reflect.Type, sample reflect.Value) *structShape {
	shape := &structShape{typ: typ, fields: map[string]shapeField{}}

	for i := range typ.NumField() {
		field := typ.Field(i)

		name, _, _ := strings.Cut(field.Tag.Get("structs"), ",")
		if !field.IsExported() || name == "" || name == "-" {
			continue
		}

		var value reflect.Value
		if sample.IsValid() {
			value = sample.Field(i)
		}

		shape.fields[name] = fieldOf(field.Type, value)
	}

	return shape
}

// fieldOf describes a field of type typ, whose value after Init is value.
func fieldOf(typ reflect.Type, value reflect.Value) shapeField {
	field := shapeField{kind: fieldScalar, shape: nil, def: nil}
	elem := typ

	if kind := typ.Kind(); kind == reflect.Pointer || kind == reflect.Slice {
		elem = typ.Elem()
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
	}

	if typ.Kind() == reflect.Slice {
		if elem.Kind() != reflect.Struct {
			field.kind = fieldList

			return field
		}

		var sample reflect.Value
		if value.IsValid() && value.Len() > 0 {
			sample = pointee(value.Index(0))
		}

		field.kind, field.shape = fieldObjects, shapeOf(elem, sample)

		return field
	}

	if elem.Kind() == reflect.Struct {
		field.kind, field.shape = fieldObject, shapeOf(elem, pointee(value))

		return field
	}

	if typ.Kind() == reflect.Map {
		field.kind = fieldMap

		return field
	}

	// Init sets a pointer to give a default, also a zero one.
	if typ.Kind() == reflect.Pointer {
		field.kind = fieldPointer
		if value = pointee(value); value.IsValid() {
			field.def = value.Interface()
		}

		return field
	}

	if value.IsValid() && !value.IsZero() {
		field.def = value.Interface()
	}

	return field
}

// pointee returns the value that value points to, or value itself when it is
// not a pointer. It returns an invalid value for a nil pointer.
func pointee(value reflect.Value) reflect.Value {
	if !value.IsValid() || value.Kind() != reflect.Pointer {
		return value
	}

	if value.IsNil() {
		return reflect.Value{}
	}

	return value.Elem()
}

// strip removes from value, an object of the shape, each field that phenix
// reads as no value, and with defaults also each field that holds its Init
// default. A field that the phenix v1 schema requires stays (see
// [structShape.required]). Keys that phenix does not know stay as written.
func (s *structShape) strip(value map[string]any, defaults bool) {
	for key, item := range value {
		field, known := s.fields[key]
		if !known {
			continue
		}

		field.stripItems(item, defaults)

		if field.unset(item, defaults) && !s.required(key, value) {
			delete(value, key)
		}
	}
}

// stripItems strips the object, or each object of the list, that item holds.
func (f shapeField) stripItems(item any, defaults bool) {
	switch f.kind {
	case fieldObject:
		if object, ok := item.(map[string]any); ok {
			f.shape.strip(object, defaults)
		}
	case fieldObjects:
		list, _ := item.([]any)
		for _, entry := range list {
			if object, ok := entry.(map[string]any); ok {
				f.shape.strip(object, defaults)
			}
		}
	case fieldScalar, fieldPointer, fieldList, fieldMap:
	}
}

// unset reports whether phenix reads item, the value of the field, as no
// value, or with defaults, whether item is the field's Init default.
func (f shapeField) unset(item any, defaults bool) bool {
	if item == nil || (defaults && f.def != nil && item == f.def) {
		return true
	}

	switch f.kind {
	case fieldScalar:
		return item == "" || item == 0 || item == false
	case fieldList, fieldObjects:
		list, ok := item.([]any)

		return ok && len(list) == 0
	case fieldMap:
		object, ok := item.(map[string]any)

		return ok && len(object) == 0
	case fieldPointer, fieldObject:
	}

	return false
}

// required reports whether the phenix v1 schema requires field key of value,
// an object of the shape, in a case where the value [structShape.strip] drops
// is valid. os_type is the Init default "linux" in most nodes.
func (s *structShape) required(key string, value map[string]any) bool {
	switch s.typ {
	case reflect.TypeFor[v1.Hardware]():
		return key == "os_type"
	case reflect.TypeFor[v1.Network]():
		return key == "interfaces"
	case reflect.TypeFor[v1.Interface]():
		switch key {
		case "mask":
			// The schema requires a mask with an address, and allows 0.
			address, _ := value["address"].(string)

			return address != ""
		case "udp_port":
			return strings.EqualFold(specString(value, "type"), "serial")
		case "vlan":
			// The schema requires a VLAN, and allows "" for an interface without one.
			return true
		}
	case reflect.TypeFor[v1.OSPF]():
		return key == "areas"
	case reflect.TypeFor[v1.Area]():
		return key == "area_networks"
	case reflect.TypeFor[v1.Ruleset]():
		return key == "rules"
	case reflect.TypeFor[v1.Rule]():
		return key == "id"
	}

	return false
}

// dropExperimentResidue removes from the nodes of an experiment's topology what phenix added at experiment creation.
// It removes the bridge defaults, fields that phenix reads as no value, and values equal to their
// Init default (v1.ExperimentSpec.Init).
// External nodes get no Init defaults, so only their unset fields go.
func dropExperimentResidue(topology map[string]any, defaultBridge string) {
	if defaultBridge == "" {
		defaultBridge = defaultBridgeName
	}

	nodes, _ := topology[keyNodes].([]any)
	shape := nodeShape()

	for _, entry := range nodes {
		node, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		dropBridgeDefaults(node, defaultBridge)
		shape.strip(node, node["external"] == nil)
	}
}

// dropBridgeDefaults removes bridge_set_in_topo from each interface of node, and the default bridge that Init gave an interface.
// Without this, a published topology lets Init keep a bridge that the user changes.
func dropBridgeDefaults(node map[string]any, defaultBridge string) {
	for _, entry := range specNodeInterfaces(node) {
		iface, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		setInTopo, flagged := iface[bridgeSetInTopoKey]
		delete(iface, bridgeSetInTopoKey)

		if !flagged || setInTopo != false {
			continue
		}

		if bridge, _ := iface["bridge"].(string); bridge == defaultBridge || bridge == defaultBridgeName {
			delete(iface, "bridge")
		}
	}
}
