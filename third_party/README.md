# lwIP source provenance

The native source is maintained in [justinwoo280/lwip](https://github.com/justinwoo280/lwip),
a fork of [heiher/lwip](https://github.com/heiher/lwip).

[`lwip.lock.json`](lwip.lock.json) pins the repository, full commit SHA and SHA256
digest of the imported `LICENSE` and `src/` tree. `lwip/` is the generated,
byte-for-byte snapshot of that commit. Keeping it in the Go module lets a normal
CGO consumer compile the package directly with `go get`.

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
The imported native source is identical to the previously validated source tree.

## Updating the dependency

Commit native changes to the lwIP fork, then synchronize its full commit SHA:

```sh
go run ./cmd/build-lwip sync-lwip --revision <full-commit-sha>
go run ./cmd/build-lwip sync-lwip --check --remote
```

The Go synchronizer fetches that exact Git commit, imports `LICENSE` and `src/`,
updates the lock and regenerates the CGO wrappers. It preserves local edits by
requiring the existing snapshot to match its lock before replacing it.
`sync-lwip --check` verifies the local snapshot without network access;
`--check --remote` also verifies it against the fork.

CI performs the remote provenance check before building. Native builds and the
publisher verify the local lock; every native manifest and `native-build.json`
record the same lwIP repository, commit and source-tree digest.

The bridge ABI, buffering, capability policy and platform configuration are
maintained in sing-lwip. The embedding configuration is in `../lwipopts.h`.
Original lwIP licensing and copyright notices are retained.
