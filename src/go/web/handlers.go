package web

import (
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	marshaler   = protojson.MarshalOptions{EmitUnpopulated: true}                      //nolint:gochecknoglobals // global marshaler
	unmarshaler = protojson.UnmarshalOptions{AllowPartial: true, DiscardUnknown: true} //nolint:gochecknoglobals // global unmarshaler
)

const sortAsc = "asc"
const defaultScreenshotSize = "215"
