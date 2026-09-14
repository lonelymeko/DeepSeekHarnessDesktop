.PHONY: dev prepare sync sync-accept smoke build package package-all test

TARGET ?= $(shell go env GOOS)/$(shell go env GOARCH)

dev: prepare
	DSH_DESKTOP_RUNTIME=runtime/current wails dev

prepare:
	./scripts/prepare-runtime.sh $(word 1,$(subst /, ,$(TARGET))) $(word 2,$(subst /, ,$(TARGET)))

sync:
	./scripts/update-upstream.sh --ref master

sync-accept:
	./scripts/update-upstream.sh --ref master --accept-breaking

# Adaptation gate: boots the prepared runtime and drives this shell's own
# handoff parsing, browser-session exchange, reverse-proxy and WebSocket-bridge
# path against it, including a chunked request body. Run it after accepting an
# upstream update, and before publishing a release built on one.
smoke: prepare
	go test -tags smoke -run TestUpstreamSmoke -v -timeout 600s .

build:
	wails build -platform $(TARGET)

package:
	./scripts/package.sh $(TARGET)

package-all:
	./scripts/package-all.sh

test:
	go test ./...
	cd frontend && npm run build
