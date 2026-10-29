CC ?= cc
CFLAGS ?= -O3
ifeq ($(OS),Windows_NT)
SHARED ?= sing-lwip.dll
SHARED_FLAGS ?= -shared
else ifeq ($(shell uname -s),Darwin)
SHARED ?= libsing-lwip.dylib
SHARED_FLAGS ?= -fPIC -dynamiclib
else
SHARED ?= libsing-lwip.so
SHARED_FLAGS ?= -fPIC -shared
endif
SOURCES := $(wildcard lwip_*.c) bridge.c

.PHONY: prepare shared generate test test-native test-upstream-udp
prepare:
	go run ./cmd/build-lwip generate --check

generate:
	go run ./cmd/build-lwip generate

shared: prepare
	$(CC) $(CPPFLAGS) $(CFLAGS) $(SHARED_FLAGS) -I. -Ithird_party/lwip/src/include -Ithird_party/lwip/src/ports/include $(SOURCES) $(LDFLAGS) -o $(SHARED)

test: prepare
	go test -race ./...

test-native:
	go run ./cmd/build-lwip test-native

test-upstream-udp:
	go run ./cmd/build-lwip test-upstream-udp
