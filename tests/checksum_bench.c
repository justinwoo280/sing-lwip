/* Standalone microbenchmark: compile with -O3, optionally forcing the generic
 * or separate-copy kernels. The compiler barrier prevents hoisting unchanged
 * inputs; noinline wrappers keep identical call boundaries in every variant.
 * Report timings from inside the process, excluding startup and allocation.
 * A warmed working set measures kernel work, not DRAM bandwidth. */
#define _POSIX_C_SOURCE 200809L
#include "../checksum.h"
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

static volatile uint32_t result_sink;

__attribute__((noinline)) static uint16_t bench_sum(const void *p, int n) {
    return sl_checksum(p, n);
}

__attribute__((noinline)) static uint16_t bench_copy(void *d, const void *p, uint16_t n) {
    return sl_checksum_copy_impl(d, p, n);
}

static uint64_t nanoseconds(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_MONOTONIC, &now)) { perror("clock_gettime"); exit(1); }
    return (uint64_t)now.tv_sec * 1000000000 + now.tv_nsec;
}

static void measure(unsigned size, unsigned src_offset, unsigned dst_offset,
                    unsigned long iterations, int copy) {
    uint8_t *source, *destination;
    if (posix_memalign((void **)&source, 64, size + 64) ||
        posix_memalign((void **)&destination, 64, size + 64)) {
        fputs("allocation failed\n", stderr); exit(1);
    }
    for (unsigned i = 0; i < size + 64; i++) source[i] = (uint8_t)(i * 37 + i / 251);
    memset(destination, 0, size + 64);
    uint8_t *src = source + src_offset, *dst = destination + dst_offset;
    uint32_t total = 0;
    for (unsigned i = 0; i < 1000; i++) {
        __asm__ volatile ("" : : : "memory");
        total += copy ? bench_copy(dst, src, (uint16_t)size) : bench_sum(src, (int)size);
    }
    uint64_t start = nanoseconds();
    for (unsigned long i = 0; i < iterations; i++) {
        __asm__ volatile ("" : : : "memory");
        total += copy ? bench_copy(dst, src, (uint16_t)size) : bench_sum(src, (int)size);
    }
    uint64_t elapsed = nanoseconds() - start;
    result_sink = total;
    printf("%s,%u,%u,%u,%lu,%.3f,%.3f\n", copy ? "copy" : "checksum", size,
           src_offset, dst_offset, iterations, (double)elapsed / iterations,
           (double)size * iterations / elapsed);
    free(source); free(destination);
}

static unsigned long number(const char *text, unsigned long maximum) {
    char *end;
    errno = 0;
    unsigned long value = strtoul(text, &end, 10);
    if (errno || !*text || *end || *text == '-' || value > maximum) {
        fprintf(stderr, "invalid argument: %s\n", text); exit(2);
    }
    return value;
}

int main(int argc, char **argv) {
    puts("mode,bytes,source_offset,destination_offset,iterations,ns_per_call,GB_per_second");
    if (argc == 6) {
        if (strcmp(argv[1], "checksum") && strcmp(argv[1], "copy")) return 2;
        unsigned size = (unsigned)number(argv[2], 65535);
        unsigned offset = (unsigned)number(argv[3], 63);
        unsigned destination = (unsigned)number(argv[4], 63);
        unsigned long iterations = number(argv[5], 1000000000UL);
        if (!size || !iterations) return 2;
        measure(size, offset, destination, iterations, !strcmp(argv[1], "copy"));
    } else if (argc == 1) {
        const unsigned sizes[] = {20, 40, 64, 128, 256, 512, 1500, 9000, 32768, 65535};
        const unsigned offsets[][2] = {{0, 0}, {1, 3}, {15, 7}, {63, 1}};
        for (unsigned s = 0; s < sizeof(sizes) / sizeof(*sizes); s++) {
            unsigned long iterations = 64UL * 1024 * 1024 / sizes[s];
            if (iterations > 2000000) iterations = 2000000;
            for (unsigned o = 0; o < sizeof(offsets) / sizeof(*offsets); o++)
                for (int copy = 0; copy <= 1; copy++)
                    measure(sizes[s], offsets[o][0], offsets[o][1], iterations, copy);
        }
    } else {
        fprintf(stderr, "usage: %s [checksum|copy SIZE SRC_OFFSET DST_OFFSET ITERATIONS]\n", argv[0]);
        return 2;
    }
    return 0;
}
