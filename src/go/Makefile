SHELL := /bin/bash

# Default version number to git commit hash if not set.
COMMIT := $(or $(COMMIT),$(shell git log -1 --format="%h"))
TAG    := $(or $(TAG),$(shell git log -1 --format="%h"))
DATE   := $(shell date -u)

SOURCES   := $(shell find . -name '*.go')
UISOURCES := $(shell find web -name '*.go' -not -path 'web/rbac/known_policy.go')
CONFIGS   := $(shell find api/config/default -name '*')
TEMPLATES := $(shell find tmpl/templates -name '*')

TUNNELSOURCES := $(shell find tunneler -name '*.go' )

THISFILE := $(lastword $(MAKEFILE_LIST))
THISDIR  := $(shell dirname $(realpath $(THISFILE)))
GOBIN    := $(THISDIR)/bin

# Prepend this repo's bin directory to our path since we'll want to
# install some build tools there for use during the build process.
PATH := $(GOBIN):$(PATH)

# Export GOBIN env variable so `go install` picks it up correctly.
export GOBIN

all:

clean:
	$(RM) bin/phenix
	$(RM) app/mock.go
	$(RM) util/mm/mock.go
	$(RM) store/mock.go
	$(RM) tmpl/bindata.go
	$(RM) util/shell/mock.go
	$(RM) web/bindata.go
	$(RM) web/proto/*.pb.go
	$(RM) web/public/docs/index.html
	$(RM) web/public/favicon.ico
	$(RM) web/public/index.html
	$(RM) -r web/public/assets
	$(RM) web/rbac/known_policy.go

.PHONY: install-build-deps
install-build-deps: bin/go-bindata bin/mockgen bin/protoc-gen-go

.PHONY: remove-build-deps
remove-build-deps:
	$(RM) bin/go-bindata
	$(RM) bin/mockgen
	$(RM) bin/protoc-gen-go

bin/go-bindata:
	go install github.com/go-bindata/go-bindata/v3/go-bindata

bin/mockgen:
	go install github.com/golang/mock/mockgen

bin/protoc-gen-go:
	go install google.golang.org/protobuf/cmd/protoc-gen-go

.PHONY: generate-bindata
generate-bindata: api/config/bindata.go tmpl/bindata.go web/bindata.go

api/config/bindata.go: $(CONFIGS) bin/go-bindata
	$(GOBIN)/go-bindata -pkg config -prefix api/config/default -o api/config/bindata.go api/config/default/...

tmpl/bindata.go: $(TEMPLATES) bin/go-bindata
	$(GOBIN)/go-bindata -pkg tmpl -prefix tmpl/templates -o tmpl/bindata.go tmpl/templates/...

web/public/docs/index.html: web/public/docs/openapi.yml
	npx redoc-cli build web/public/docs/openapi.yml -o web/public/docs/index.html --title 'phenix API'

web/bindata.go: web/public/docs/index.html web/public/index.html web/public/vnc.html bin/go-bindata
	$(GOBIN)/go-bindata -pkg web -prefix web/public -o web/bindata.go web/public/...

.PHONY: generate-mocks
generate-mocks: app/mock.go store/mock.go util/mm/mock.go util/shell/mock.go

app/mock.go: app/app.go bin/mockgen
	$(GOBIN)/mockgen -self_package phenix/app -destination app/mock.go -package app phenix/app App

store/mock.go: store/store.go bin/mockgen
	$(GOBIN)/mockgen -self_package phenix/store -destination store/mock.go -package store phenix/store Store

util/mm/mock.go: util/mm/mm.go bin/mockgen
	$(GOBIN)/mockgen -self_package phenix/util/mm -destination util/mm/mock.go -package mm phenix/util/mm MM

util/shell/mock.go: util/shell/shell.go bin/mockgen
	$(GOBIN)/mockgen -self_package phenix/util/shell -destination util/shell/mock.go -package shell phenix/util/shell Shell

.PHONY: generate-protobuf
generate-protobuf: web/proto/experiment.pb.go web/proto/host.pb.go web/proto/log.pb.go web/proto/user.pb.go web/proto/role.pb.go web/proto/vm.pb.go

web/proto/experiment.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/experiment.proto

web/proto/host.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/host.proto

web/proto/log.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/log.proto

web/proto/user.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/user.proto

web/proto/role.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/role.proto

web/proto/vm.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/vm.proto

.PHONY: go-generate
go-generate: web/rbac/known_policy.go

web/rbac/known_policy.go: $(UISOURCES)
	go generate web/rbac/known_policy_gen.go

bin/phenix: $(SOURCES) generate-bindata generate-protobuf go-generate
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -X 'phenix/version.Tag=$(TAG)' -X 'phenix/version.Date=$(DATE)' -s -w" -trimpath -o bin/phenix main.go

bin/phenix-debug: $(SOURCES) generate-bindata generate-protobuf go-generate
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -gcflags "all=-N -l" -trimpath -o bin/phenix-debug main.go

.PHONY: phenix-tunneler
phenix-tunneler: bin/phenix-tunneler-linux-amd64 bin/phenix-tunneler-darwin-arm64 bin/phenix-tunneler-windows-amd64.exe

bin/phenix-tunneler-linux-amd64: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-linux-amd64 $(TUNNELSOURCES)

bin/phenix-tunneler-darwin-arm64: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-darwin-arm64 $(TUNNELSOURCES)

bin/phenix-tunneler-windows-amd64.exe: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-windows-amd64.exe $(TUNNELSOURCES)
