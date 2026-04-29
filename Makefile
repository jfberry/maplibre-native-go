MLN_FFI_DIR ?= $(HOME)/dev/maplibre-native-ffi

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
RPATH_FLAG := -Wl,-rpath,$(MLN_FFI_DIR)/build
else
RPATH_FLAG := -Wl,-rpath=$(MLN_FFI_DIR)/build
endif

export CGO_CFLAGS  := -I$(MLN_FFI_DIR)/include
export CGO_LDFLAGS := -L$(MLN_FFI_DIR)/build -lmaplibre_native_abi $(RPATH_FLAG)

.PHONY: native build test poc bench env clean

native:
	cd $(MLN_FFI_DIR) && mise run build

build:
	go build ./...

test:
	go test -race ./...

poc:
	go run ./cmd/poc

bench:
	go run ./cmd/bench

# `eval $(make env)` to source the cgo flags into your shell.
env:
	@echo 'export CGO_CFLAGS="$(CGO_CFLAGS)"'
	@echo 'export CGO_LDFLAGS="$(CGO_LDFLAGS)"'

clean:
	go clean ./...
