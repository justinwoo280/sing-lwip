/* Native-order, non-inverted Internet sums for lwIP's LWIP_CHKSUM hook.
 * No loads outside the supplied range, no alignment requirements, no native ABI
 * additions. The compiler manages the platform ABI around generated inline asm. */
#ifndef SING_LWIP_CHECKSUM_H
#define SING_LWIP_CHECKSUM_H
#include <stdint.h>
#include <string.h>
#include "checksum_generated.h"

/* Keep each chunk <= 65536 bytes: even all-ones input sums to < 2^31.
 * memcpy permits unaligned input without violating C effective-type rules. */
static inline uint32_t sl_checksum_scalar(const uint8_t *p, unsigned n) {
    uint32_t sum = 0;
    while (n >= 16) {
        uint32_t a, b, c, d;
        memcpy(&a, p, 4); memcpy(&b, p+4, 4);
        memcpy(&c, p+8, 4); memcpy(&d, p+12, 4);
        sum += (a & 65535) + (a >> 16) + (b & 65535) + (b >> 16);
        sum += (c & 65535) + (c >> 16) + (d & 65535) + (d >> 16);
        p += 16; n -= 16;
    }
    while (n >= 2) {
        uint16_t word;
        memcpy(&word, p, 2);
        sum += word; p += 2; n -= 2;
    }
    if (n) {
        uint16_t word = 0;
        memcpy(&word, p, 1);
        sum += word;
    }
    return sum;
}

static inline uint16_t sl_checksum(const void *data, int length) {
    const uint8_t *p = (const uint8_t *)data;
    uint32_t sum = 0;
    while (length > 0) {
        unsigned n = length > 65536 ? 65536 : (unsigned)length;
        length -= (int)n;
#ifdef SL_CHECKSUM_BLOCKS
        if (n >= 64) {
            sum += sl_checksum_blocks(&p, n / 64);
            n %= 64;
        }
#endif
        sum += sl_checksum_scalar(p, n);
        p += n;
        sum = (sum & 65535) + (sum >> 16);
        sum = (sum & 65535) + (sum >> 16);
    }
    return (uint16_t)sum;
}

/* lwIP's copy hook has memcpy semantics: the ranges must not overlap. The
 * fused kernels load each source block once, then both store and sum it. */
static inline uint16_t sl_checksum_copy_impl(void *destination, const void *source, uint16_t length) {
    if (!length) return 0;
#ifdef SL_CHECKSUM_COPY_BLOCKS
    const uint8_t *p = (const uint8_t *)source;
    uint8_t *d = (uint8_t *)destination;
    unsigned n = length;
    uint32_t sum = 0;
    if (n >= 64) {
        sum = sl_checksum_copy_blocks(&d, &p, n / 64);
        n %= 64;
    }
    /* The bounded short tail is small enough to stay in registers/cache. */
    memcpy(d, p, n);
    sum += sl_checksum_scalar(p, n);
    sum = (sum & 65535) + (sum >> 16);
    sum = (sum & 65535) + (sum >> 16);
    return (uint16_t)sum;
#else
    memcpy(destination, source, length);
    return sl_checksum(destination, length);
#endif
}
#endif
