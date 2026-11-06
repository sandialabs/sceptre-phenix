package web

import (
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	marshaler   = protojson.MarshalOptions{EmitUnpopulated: true}                      //nolint:gochecknoglobals // global marshaler
	unmarshaler = protojson.UnmarshalOptions{AllowPartial: true, DiscardUnknown: true} //nolint:gochecknoglobals // global unmarshaler
)

const sortAsc = "asc"
const defaultScreenshotSize = "200"

// sortColumn is the column a REST list is sorted by: none unless both sortCol
// and sortDir are given.
func sortColumn(col, dir string) string {
	if dir == "" {
		return ""
	}

	return col
}
