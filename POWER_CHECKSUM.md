# POWER checksum kernels and measurements

## Implementation

`cmd/build-lwip/checksum.go` generates the VSX kernels in
`checksum_generated.h`; do not edit the generated header directly.
The existing native ABI and lwIP checksum/copy hooks are unchanged.

- Process complete 64-byte blocks, with four independent `VMSUMUHM`
  accumulators. Each block contains 32 native-order unsigned halfwords.
- Each kernel call handles at most 64 KiB, whose all-ones sum is 2,147,450,880.
  Neither the accumulators nor the signed saturating final `VSUMSWS` overflow.
- `LXV` loads and `STXV` stores tolerate byte misalignment without accessing
  outside a full block. The C tail handles the remaining bytes.
- Baseline Linux Power builds keep the kernel in a POWER9-targeted function.
  A cached, thread-safe HWCAP check requires AltiVec, VSX and ISA 3.0 before
  calling it. POWER8 falls back to portable C. Missing capabilities do not
  enable the kernel. Explicit POWER9-or-newer builds can inline it.
- Small headers take a short scalar path before the out-of-line call machinery.

## Reproduce the microbenchmark

On a native ppc64le system with GCC or Clang:

```sh
cc -O3 -mcpu=power8 tests/checksum_bench.c -o /tmp/checksum-vsx
cc -O3 -mcpu=power8 -DSING_LWIP_CHECKSUM_GENERIC \
  tests/checksum_bench.c -o /tmp/checksum-generic
taskset -c 2 /tmp/checksum-generic
taskset -c 2 /tmp/checksum-vsx

# A single case for hardware counters: mode, bytes, source offset,
# destination offset and iteration count.
sudo taskset -c 2 perf stat -r 5 -e '{cycles:u,instructions:u}' \
  /tmp/checksum-vsx copy 1500 1 3 5000000
```

The program reports CSV with ns/call and decimal GB/s. Its default table
covers 20 to 65,535 bytes, aligned and independently unaligned pointers.
It warms buffers before timing, uses identical noinline call boundaries and
compiler barriers against hoisting, and excludes process startup/allocation
from its internal timer. Compare interleaved runs rather than running one
variant's entire measurement series first. `SING_LWIP_CHECKSUM_COPY_SEPARATE`
provides a third variant: optimized checksum with a separate libc copy.

## Native POWER10 observations

Measured on an OpenStack/KVM POWER10 guest with four vCPUs, Ubuntu 24.04,
GCC 13.3 and Clang 18.1. Both variants used `-O3 -mcpu=power8`, allowing the
compiler to optimize the portable baseline. The optimized variant used runtime
dispatch. Processes were pinned to guest vCPU 2; this is not host CPU isolation.

The following are medians of seven interleaved runs. Source/destination offsets
were 1 and 3 bytes. Values are kernel timings, not whole-stack throughput.

| Compiler | Operation | Bytes | Portable ns/call | VSX ns/call | Speedup |
|---|---|---:|---:|---:|---:|
| GCC | checksum | 64 | 9.52 | 10.14 | 0.94x |
| GCC | checksum | 1500 | 81.67 | 40.38 | 2.02x |
| GCC | copy + checksum | 1500 | 107.42 | 46.77 | 2.30x |
| GCC | checksum | 9000 | 415.89 | 222.56 | 1.87x |
| GCC | copy + checksum | 9000 | 550.29 | 232.99 | 2.36x |
| GCC | checksum | 65535 | 2954.82 | 1601.38 | 1.85x |
| GCC | copy + checksum | 65535 | 3890.80 | 1627.17 | 2.39x |
| Clang | checksum | 64 | 15.81 | 12.47 | 1.27x |
| Clang | checksum | 1500 | 104.61 | 48.09 | 2.18x |
| Clang | copy + checksum | 1500 | 131.55 | 48.85 | 2.69x |
| Clang | checksum | 9000 | 411.60 | 231.68 | 1.78x |
| Clang | copy + checksum | 9000 | 543.61 | 237.62 | 2.29x |
| Clang | checksum | 65535 | 2929.14 | 1608.82 | 1.82x |
| Clang | copy + checksum | 65535 | 3843.48 | 1623.48 | 2.37x |

For 5,000,000 calls at 1500 bytes, paired cycles/instructions events ran 100%
of the measured interval. Five-run `perf stat` totals (including startup and
the benchmark harness) corroborated reduced work:

| Compiler / operation | Portable instructions | VSX instructions | Portable cycles | VSX cycles |
|---|---:|---:|---:|---:|
| GCC checksum | 4.704 billion | 2.128 billion | 1.598 billion | 0.785 billion |
| GCC copy + checksum | 6.224 billion | 2.653 billion | 2.096 billion | 0.915 billion |
| Clang checksum | 6.331 billion | 2.476 billion | 2.030 billion | 0.940 billion |
| Clang copy + checksum | 7.842 billion | 2.791 billion | 2.549 billion | 0.952 billion |

Short packets do not universally improve, as the GCC 64-byte case illustrates.
The warm working set also makes these numbers unsuitable as DRAM bandwidth
measurements. Real networking additionally includes allocation, Go/native
crossings, scheduling, packet I/O and the rest of TCP/IP.

## POWER10-specific experiment

A separate prototype replaced four `LXV`/`STXV` operations with two
`LXVP`/`STXVP` pairs. GCC and Clang correctness/guard tests passed, but native
POWER10 measurements did not show a consistent benefit. Some small aligned
cases improved while unaligned 1500-byte fused copies were about 10% slower.
The shipped kernel therefore uses the POWER9 instruction sequence on POWER10
as well; a separate POWER10 branch would require a demonstrated improvement.

## Correctness coverage

`tests/checksum.c` checks independent byte-wise results, all 64 source alignment
offsets, independent copy destination offsets, zero/odd lengths, overflow
patterns, 64-KiB chunk boundaries, read-only source and guard pages, and real
IPv4/IPv6 pbuf-chain/pseudo-header sums. Optional `SING_LWIP_TEST_VECTOR=0|1`
asserts runtime dispatch before running the suite.

Native POWER10 GCC/Clang builds pass these checks and the CGO/PureGo TCP, UDP,
GSO, reassembly and lifecycle suites. GCC ASan/UBSan and Go race tests pass.
LinuxONE native VX and protocol-stack/sanitizer regressions also pass.
Zig 0.15.2 builds of both little- and big-endian C fixtures pass under QEMU
POWER8 (portable fallback) and POWER9 (VSX). Big-endian emulation is a
correctness check, not a performance result or a newly published Go platform.
