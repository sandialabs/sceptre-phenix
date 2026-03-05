//go:build tools
// +build tools

package tools

import (
	_ "github.com/golang/mock/mockgen"
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
)
