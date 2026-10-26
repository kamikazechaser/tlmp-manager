BIN        := tlmp-manager
BUILD_CONF := CGO_ENABLED=0 GOOS=linux GOARCH=amd64
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
CDN_BASE   := https://files.download.trucklinemp.com/client
API_BASE   := https://api.trucklinemp.com
SHIM_CC    ?= x86_64-w64-mingw32-gcc
SHIM_DLL   := internal/shim/dstorage.dll

.PHONY: build release run test clean shim

clean:
	rm -rf build ${SHIM_DLL}

# Go builds need this DLL, because internal/shim embeds it.
shim: ${SHIM_DLL}

${SHIM_DLL}: shim/dstorage.c shim/dstorage.def
	${SHIM_CC} -shared -O2 -s -Wall -nostdlib -Wl,--entry,DllMain -Wl,--no-insert-timestamp \
		-o $@ $^ -lkernel32 -luser32

build: ${SHIM_DLL}
	@mkdir -p build
	${BUILD_CONF} go build \
		-ldflags="-X main.version=${VERSION} -X main.cdnBase=${CDN_BASE} -X main.apiBase=${API_BASE} -s -w" \
		-o build/${BIN} \
		./cmd/tlmp-manager

release: build
	cp build/${BIN} build/${BIN}-linux-amd64
	cp ${SHIM_DLL} build/dstorage.dll
	cd build && sha256sum ${BIN}-linux-amd64 dstorage.dll > checksums.txt

test: ${SHIM_DLL}
	go vet ./...
	go test ./...

run: ${SHIM_DLL}
	${BUILD_CONF} DEBUG=true go run ./cmd/tlmp-manager
