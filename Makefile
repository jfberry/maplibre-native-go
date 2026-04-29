MLN_FFI_DIR ?= $(HOME)/dev/maplibre-native-ffi

.PHONY: native build test poc clean

native:
	cd $(MLN_FFI_DIR) && mise run build

build:
	go build ./...

test:
	go test ./...

poc:
	go run ./cmd/poc

clean:
	go clean ./...
