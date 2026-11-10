#ifndef SING_LWIP_BRIDGE_H
#define SING_LWIP_BRIDGE_H
#include <stdint.h>

/* i386 FFI callers may use the legacy four-byte stack alignment. Realign at
 * the exported boundary before calling code that uses aligned SSE locals. */
#if defined(__i386__) && (defined(__GNUC__) || defined(__clang__))
#define SL_ALIGN_STACK __attribute__((force_align_arg_pointer))
#else
#define SL_ALIGN_STACK
#endif

#ifdef _WIN32
#define SL_API __declspec(dllexport) SL_ALIGN_STACK
#else
#define SL_API SL_ALIGN_STACK
#endif

/* ABI 2. All entry points, including timers, require the process core lock.
 * Config (32 bytes): LE mtu, LE max-connections, IPv4[4], LE flags, IPv6[16].
 * Config flags: bit 0 enables TCP GSO on this netif. All other bits are reserved.
 * Address (40 bytes): src[16], dst[16], LE src-port, LE dst-port, IP version,
 * transport, reserved[2]. No function retains a caller's buffer pointer.
 * Events: LE kind:u32, length:u32, id:u64, followed by payload.
 * kind=1 TCP accept / 2 UDP accept / 3 readiness. Packets use sl_output.
 * Read/write results: -1 would-block, -2 closed, -3 reset, -4 invalid,
 * -5 out-of-memory, -6 receive closed. TCP read 0 is EOF; UDP 0 is valid.
 */
SL_API uint32_t sl_abi(void);
SL_API uint64_t sl_create(const void *config);
SL_API void sl_destroy(uint64_t stack);
SL_API int32_t sl_input(uint64_t stack, const void *data, uint32_t len);
SL_API void sl_tick(void);
SL_API uint32_t sl_drain(uint64_t stack, void *data, uint32_t cap);
/* Output records: LE length:u32, gso-size:u16, reserved:u16, packet bytes.
 * gso-size is the TCP payload size per wire segment (0 for ordinary packets).
 * IP/transport checksums are complete, including on an aggregate. */
SL_API uint32_t sl_output(uint64_t stack, void *data, uint32_t cap);
SL_API int32_t sl_read(uint64_t id, void *data, uint32_t cap, void *address);
SL_API int32_t sl_write(uint64_t id, const void *data, uint32_t len, const void *address);
SL_API int32_t sl_shutdown(uint64_t id, uint32_t read, uint32_t write);
SL_API void sl_close(uint64_t id, uint32_t abort);
#endif
