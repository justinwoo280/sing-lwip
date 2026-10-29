# sing-lwip

[![Native build](https://github.com/justinwoo280/sing-lwip/actions/workflows/native-build.yml/badge.svg)](https://github.com/justinwoo280/sing-lwip/actions/workflows/native-build.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/justinwoo280/sing-lwip.svg)](https://pkg.go.dev/github.com/justinwoo280/sing-lwip)

Go bindings for [HEV lwIP](https://github.com/heiher/lwip), with CGO and PureGo backends for transparent packet processing.

Native fixes are maintained in [justinwoo280/lwip](https://github.com/justinwoo280/lwip).
The [source lock](third_party/lwip.lock.json) pins the exact fork commit; the Go synchronizer imports the C sources used by all native builds.

sing-lwip accepts IPv4/IPv6 packets and exposes TCP streams and UDP datagrams. The application supplies packet I/O,
connection routing and upstream dialing. It is independent of sing-tun and sing-box; those projects can use it as a backend.

## Native libraries and Go bindings

The **C toolchain** compiles lwIP and `bridge.c` into native code. The build tool packages that code as a static
archive (`.a`) or links it into a shared library (`.so`, `.dylib`, `.dll`). **CGO** and **PureGo** describe how the Go
application calls this native code. PureGo is an FFI library that loads and calls the shared library; it does not compile C code.

| Go binding mode | How sing-lwip native code is used |
|---|---|
| CGO, bundled sources | The Go build invokes a C compiler and links the compiled native code into the application. |
| CGO, `with_lwip_prebuilt` | The target linker links the prebuilt `libsing-lwip.a` into the application. |
| PureGo, `with_purego` | Go compiles the application; at runtime PureGo loads the separately C-built shared library. |

CGO supports both static and dynamic native linking in general; this package's documented CGO modes use the bundled
sources or a static archive. Static linkage here refers to sing-lwip; system-library dependencies depend on the target toolchain.

## Build targets

| Target | CGO binding | PureGo loader | C-built native artifacts |
|---|---|---|---|
| Linux glibc: amd64, arm64, 386, arm, loong64, ppc64le, riscv64 | Yes | Yes | `.a`, `.so` |
| Linux glibc: mipsle, mips64le | Yes | — | `.a`, `.so` |
| Linux musl: amd64, arm64, 386, arm, loong64, ppc64le, riscv64, mipsle | Yes | — | `.a` |
| macOS: amd64, arm64 | Yes | Yes | `.a`, `.dylib` |
| Windows: 386, amd64, arm64 | MinGW | Yes | `.a`, `.dll` |
| Android: 386, amd64, arm, arm64 | Yes | — | `.a` |
| iOS/tvOS: arm64 devices; amd64/arm64 simulators | Yes | — | `.a` |
| FreeBSD: amd64, arm64 | Yes | Yes | `.a`, `.so` |

Go **1.24 or newer** is required. CI checks Go 1.24/1.25 compatibility and builds the release matrix with Go 1.26.
Published ARM32 libraries target **ARMv7** (`GOARM=7`).
Linux ARM32 artifacts use the hard-float (armhf) ABI.
Published MIPS libraries also use the hard-float ABI (`GOMIPS=hardfloat` / `GOMIPS64=hardfloat`).
Android artifacts target API 23; Apple artifacts target macOS 12 / iOS 15 / tvOS 15.
Musl and mobile distributions use CGO static linking.

The workflow runs native tests on Linux amd64/arm64, macOS amd64/arm64 and Windows 386/amd64/arm64.
Other Linux targets run under QEMU where a matching runtime is available. Linux loong64 glibc, Android,
iOS/tvOS and FreeBSD currently have compile checks rather than native-OS runtime coverage.
See the [Actions results](https://github.com/justinwoo280/sing-lwip/actions/workflows/native-build.yml) for the exact commit being used.

## Installation

### CGO: compile bundled C sources

The source branch includes the native C sources. A normal CGO build compiles them with the application:

```sh
go get github.com/justinwoo280/sing-lwip@main
CGO_ENABLED=1 go build ./...
```

Cross-compilation requires a C compiler and sysroot for the target. Windows CGO uses MinGW;
Android uses the NDK; Apple platforms use Xcode and the corresponding SDK.

### CGO: link a prebuilt static library

The `go` branch contains the Go bindings and generated platform-library dependencies:

```sh
go get github.com/justinwoo280/sing-lwip@go
CGO_ENABLED=1 go build -tags with_lwip_prebuilt ./...
```

`go get` records an immutable module version. The generated `go.mod` pins every `lib/*` submodule to the exact
native-library commit, and build constraints select the library for the current target. Import the root package normally;
no additional `all` package is required.

For Linux musl, select a musl compiler and add `with_musl`:

```sh
CC=musl-gcc CGO_ENABLED=1 go build -tags 'with_lwip_prebuilt with_musl' ./...
```

Apple simulator builds use `iossimulator`, or `tvos,tvossimulator` for tvOS. Device tvOS builds use `tvos`.
CGO still needs a compatible target compiler/linker when using a prebuilt archive.

### PureGo: load a C-built shared library

The following command builds the Go executable using the PureGo binding. At runtime, it loads the matching native
shared library built by the C toolchain and distributed separately:

```sh
go get github.com/justinwoo280/sing-lwip@go
CGO_ENABLED=0 go build -tags with_purego -o app ./cmd/app
```

Distribute the matching library beside the executable:

| Platform | Library filename |
|---|---|
| Linux / FreeBSD | `libsing-lwip.so` |
| macOS | `libsing-lwip.dylib` |
| Windows | `sing-lwip.dll` |

Alternatively, set `SING_LWIP_LIBRARY` to the library's full path. Loading failures and ABI mismatches return an error.
The current native ABI is **2**. Update the Go module and its native library together.

For example, retrieve the Linux amd64 glibc library pinned by the application's module graph:

```sh
go mod download github.com/justinwoo280/sing-lwip/lib/linux_amd64_gnu
LIB_DIR=$(go list -m -f '{{.Dir}}' github.com/justinwoo280/sing-lwip/lib/linux_amd64_gnu)
cp "$LIB_DIR/libsing-lwip.so" .
```

On Windows, use the `windows_amd64` or `windows_arm64` submodule and copy `sing-lwip.dll` alongside the application.
The `lib/<target>/SHA256SUMS` file covers the library and its metadata. Native build archives are also available as
workflow artifacts.

FreeBSD PureGo builds with CGO disabled require the upstream PureGo compiler flag:

```sh
CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build \
  -tags with_purego \
  -gcflags=github.com/ebitengine/purego/internal/fakecgo=-std ./...
```

For Linux ARMv7 PureGo, select the armhf dynamic loader explicitly (Go 1.24 through 1.26 use the soft-float loader name
by default when CGO is disabled):

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build \
  -tags with_purego -ldflags '-I /lib/ld-linux-armhf.so.3' ./...
```

Linux RISC-V PureGo likewise needs `-ldflags '-I /lib/ld-linux-riscv64-lp64d.so.1'` for the published LP64D library.

## API overview

- `New(Options)` creates an independent network interface. All instances share a serialized native core.
- `Input(packet)` accepts a complete IPv4/IPv6 packet.
- `ReadPacketBatch(buffer, packets)` returns `Packet{Data, GSOSize}` and reuses the metadata slice.
- `Options.Accept` receives TCP/UDP `Conn` objects on Go goroutines. Use `Conn.IsUDP()` to distinguish them.
- TCP implements `net.Conn`, deadlines, `CloseRead`, `CloseWrite` and upstream handshake feedback.
- UDP uses `ReadPacket` / `WritePacket` with per-datagram source and destination addresses.
- `Source()` / `Destination()` expose the original endpoints; `ResetUDP` clears native UDP sessions.
- `Close` releases the instance and wakes blocked operations.

The default native connection limit is 16384 per instance, configurable through `Options.MaxConnections`.
Callbacks from C enqueue events for Go; they do not call back into Go directly. Native code never retains a Go buffer pointer.

The native core reuses allocations through a lazy, process-wide cache capped at
4 MiB (at most 2 MiB per size class), in addition to the large receive-buffer cache.
Buffers return to the cache only after their final native reference is released.
Closing the last stack releases both caches. This also covers transmit pbufs and
output records, avoiding repeated allocation and page reclamation during transfers.

### Output buffers and TCP GSO

`ReadPacketBatch` needs a buffer of at least **65543 bytes**. Packet data borrows that buffer until it is reused.
The native output header is little-endian `length:u32, gso_size:u16, reserved:u16`, followed by the IP packet.

`Options.TCPGSO` defaults to false. When enabled, the output consumer must software-segment aggregates or submit
matching device offload metadata. IP/TCP checksums are complete; a device writer converts its own copy to a partial checksum.
Wire-sized packets can also carry `GSOSize` and should be treated as ordinary packets.

The negotiated TCP MSS remains separate from the aggregation limit. GSO is isolated per interface and preserves
window, retransmission, FIN and PSH semantics. `ReadPackets` remains available with TCP GSO disabled.

## Building native libraries

The build CLI is written in Go and lives in `cmd/build-lwip`. It invokes the target C compiler, archiver and linker
(for example Zig CC, Clang or GCC) to build the static and shared libraries from the same C sources.

```sh
git clone https://github.com/justinwoo280/sing-lwip.git
cd sing-lwip
go run ./cmd/build-lwip generate --check

# Native build on the current host:
go run ./cmd/build-lwip build \
  --target "$(go env GOOS)/$(go env GOARCH)" --output dist/native --tests

# Cross-build with Zig:
go run ./cmd/build-lwip build --target linux/amd64 \
  --cc 'zig cc -target x86_64-linux-gnu.2.17' --ar 'zig ar' \
  --output dist/linux_amd64_gnu --tests
```

The output includes native libraries, C ABI headers, licenses, an ABI test fixture, a source/toolchain manifest,
SHA256SUMS and a ZIP (Windows) or tar.gz package. `--tests` also builds target-specific test executables.
`--static-only` is used for musl/mobile packaging and is automatic for iOS/tvOS.
FreeBSD/arm64 CGO executable linking requires GNU `ld.bfd`; the Linux cross-build checks the CGO package and builds
the PureGo test executable. Run CGO tests on FreeBSD with `devel/binutils` installed.

`CC`, `AR`, `CFLAGS`, `CPPFLAGS` and `LDFLAGS` are supported. The compiler selects the actual libc;
`--libc gnu|musl` records the Linux artifact variant. The builder verifies the emitted binary's architecture and byte order.

### Windows MSVC DLLs with xwin

An MSVC DLL is also suitable for the PureGo backend. Install the CRT and SDK with xwin, then build using Clang/LLD:

```sh
xwin --accept-license --arch x86_64,aarch64 --cache-dir .cache/xwin \
  splat --copy --output toolchains/xwin
go run ./cmd/build-lwip windows --sdk toolchains/xwin \
  --arch arm64 --output dist/windows_arm64/sing-lwip.dll
```

Use `--arch amd64` for x64. This builder uses the static MSVC CRT. The regular `build` command and published
Windows static archives use MinGW for compatibility with Go's CGO toolchain.

## Continuous integration and publishing

The [native build workflow](.github/workflows/native-build.yml) follows this flow:

```text
main / dev push
  ├── fetch and verify the locked lwIP fork commit
  ├── validate generated sources and publisher tests
  ├── compile C static/shared libraries for the platform/libc matrix
  ├── run native or QEMU tests where available
  └── verify the complete artifact set
       └── publish go / go_dev
            ├── lib/<target> native-library modules
            ├── generated CGO platform imports and pinned go.mod
            └── native-build.json source/library commit mapping
```

Pull requests build and test without publishing. The output branches do not trigger another native build.
Publishing appends normal commits, validates the module graph locally, checks every artifact's lwIP fork revision, source fingerprint,
ABI and hashes, and pushes the complete result once. Missing targets or failed jobs prevent publication.
An outdated workflow cannot publish after its source branch has moved.

CI caches Go modules/build outputs, Zig's toolchain/compiler cache, and target/SDK-specific ccache data for Apple and Android.
Manifests and module versions are regenerated for each source commit; prebuilt output directories are not reused as release inputs.

To run the publisher against a complete downloaded artifact set:

```sh
go run ./cmd/build-lwip publish --artifacts artifacts --branch go --source-branch main
```

`--dry-run` prepares and validates the commits without pushing. `go_dev` contains builds from `dev`.
For downstream releases, pin the resolved module version rather than repeatedly resolving a moving branch.

## Testing and development

```sh
go test -race ./...
go run ./cmd/build-lwip test-native
go run ./cmd/build-lwip test-upstream-udp

SING_LWIP_LIBRARY="$PWD/dist/native/libsing-lwip.so" \
SING_LWIP_ABI_FIXTURE="$PWD/dist/native/abi-fixture.so" \
CGO_ENABLED=0 go test -count=1 -tags with_purego ./...
```

Use `.dylib` or `.dll` fixture/library filenames on macOS or Windows. PureGo tests use `-count=1` so a replaced
shared library is actually loaded. Tests operate on in-memory packets; they do not create TUN devices or modify routes.
Native regressions require an ASan/UBSan/LSan-capable C compiler. The ABI fixture checks full-width handles,
pointer/length arguments and 32-bit calling conventions.

Update native code in the lwIP fork, then import the resulting commit:

```sh
go run ./cmd/build-lwip sync-lwip --revision <full-commit-sha>
go run ./cmd/build-lwip sync-lwip --check --remote
```

Synchronization updates the source lock, bundled snapshot and CGO source fingerprints together.
Fork commits and the independent UDP, fragment-isolation and GSO changes are documented in
[third_party/README.md](third_party/README.md).

## License

BSD-3-Clause. Vendored lwIP retains its original copyright notices and [license](third_party/lwip/LICENSE).
