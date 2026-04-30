MLN_FFI_DIR ?= $(HOME)/dev/maplibre-native-ffi

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
RPATH_FLAG := -Wl,-rpath,$(MLN_FFI_DIR)/build
else
RPATH_FLAG := -Wl,-rpath=$(MLN_FFI_DIR)/build
endif

# pkg-config resolves -I, -L, -l for libmaplibre-native-c. We still set rpath
# explicitly so the dylib is found at run time without DYLD_LIBRARY_PATH.
export PKG_CONFIG_PATH := $(MLN_FFI_DIR)/build/pkgconfig:$(PKG_CONFIG_PATH)
export CGO_LDFLAGS     := $(RPATH_FLAG)

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
	@echo 'export PKG_CONFIG_PATH="$(PKG_CONFIG_PATH)"'
	@echo 'export CGO_LDFLAGS="$(CGO_LDFLAGS)"'

clean:
	go clean ./...
