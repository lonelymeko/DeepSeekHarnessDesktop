.PHONY: dev prepare sync sync-accept build package package-all test

TARGET ?= $(shell go env GOOS)/$(shell go env GOARCH)

dev: prepare
	DSH_DESKTOP_RUNTIME=runtime/current wails dev

prepare:
	./scripts/prepare-runtime.sh $(word 1,$(subst /, ,$(TARGET))) $(word 2,$(subst /, ,$(TARGET)))

sync:
	./scripts/update-upstream.sh --ref master

sync-accept:
	./scripts/update-upstream.sh --ref master --accept-breaking

build:
	wails build -platform $(TARGET)

package:
	./scripts/package.sh $(TARGET)

package-all:
	./scripts/package-all.sh

test:
	go test ./...
	cd frontend && npm run build
