# lwIP source provenance

The native source is maintained in [justinwoo280/lwip](https://github.com/justinwoo280/lwip),
a fork of [heiher/lwip](https://github.com/heiher/lwip).

[`lwip.lock.json`](lwip.lock.json) pins the repository, full commit SHA and SHA256
digest of the downloaded `LICENSE` and `src/` tree. `lwip/` is an ignored build
cache, not part of this repository or the published Go module. Native builders
fetch the exact locked commit when this cache is absent and verify its contents
on every reuse. They never resolve a branch or tag in place of the pinned SHA.

Before a plain source-CGO `go build` or `go test`, prepare the cache with
`go run ./cmd/build-lwip sync-lwip`. The Go build itself cannot download C sources.
Prebuilt CGO and PureGo consumers do not need the source cache.

## Current pin

- Base: [`495671e59d736ef59f6cfa7dc9d35b7359921062`](https://github.com/heiher/lwip/commit/495671e59d736ef59f6cfa7dc9d35b7359921062)
- Pinned fork commit: [`02541e94fc3b0d4cbd0db33d7c610bd7f1b1086d`](https://github.com/justinwoo280/lwip/commit/02541e94fc3b0d4cbd0db33d7c610bd7f1b1086d)

The fork contains three independently reviewable changes:

1. [`adf1ac7`](https://github.com/justinwoo280/lwip/commit/adf1ac750c3b23218f2e2918f0699234c1344f12):
   UDP pretend-PCB lookup respects the ingress netif. Includes a standalone
   IPv4/IPv6 C regression and its build instructions.
2. [`c31a016`](https://github.com/justinwoo280/lwip/commit/c31a0166c31b0a4490b45a98c99fbd801e0c1afc):
   per-netif IPv4/IPv6 fragment reassembly identity and teardown APIs. This is an
   embedding-specific isolation policy whose semantics should be discussed with upstream.
3. [`02541e9`](https://github.com/justinwoo280/lwip/commit/02541e94fc3b0d4cbd0db33d7c610bd7f1b1086d):
   separate TCP GSO aggregation limits from negotiated wire MSS and let tagged
   aggregates reach the output device without IP fragmentation.

Each change was built independently against the pinned upstream base. The UDP
regression misroutes four datagrams per IP family before its fix and none after.
The locked tree digest covers every downloaded source file and its relative path.

## Updating the dependency

Commit native changes to the lwIP fork, then synchronize its full commit SHA:

```sh
go run ./cmd/build-lwip sync-lwip --revision <full-commit-sha>
go run ./cmd/build-lwip sync-lwip --check --remote
```

With `--revision`, the Go synchronizer fetches that exact Git commit, prepares
`LICENSE` and `src/`, updates the lock and regenerates the CGO wrappers. Commit
only the lock and generated wrappers; the cache stays ignored. Existing cached
sources must match their current lock before a pin update can replace them.

Without flags, `sync-lwip` prepares or verifies the cache without changing the
lock or generated files. `--check` verifies an existing cache offline;
`--check --remote` also verifies the remote commit and works without a local
cache, creating no files. `generate --check` prepares missing sources, then checks
the committed generated files without rewriting them.

A mismatched cache is rejected rather than silently overwritten. If a checkout
changes the lock, preserve any local source edits in the fork, remove the stale
`third_party/lwip/` directory and prepare the newly locked sources again.

CI starts from the lock and fetches its exact source commit before compiling.
Native builds and the publisher verify the downloaded tree; every native manifest and `native-build.json`
record the same lwIP repository, commit and source-tree digest.

The bridge ABI, buffering, capability policy and platform configuration are
maintained in sing-lwip. The embedding configuration is in `../lwipopts.h`.
Original lwIP licensing and copyright notices are retained.
