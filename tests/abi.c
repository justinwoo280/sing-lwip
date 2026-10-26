/* Native ABI fixture: catches truncated uint64 handles, shifted pointer/length
 * arguments and missing stack arguments on 32-bit FFI paths. No lwIP core. */
#include "bridge.h"
#define HANDLE UINT64_C(0xa123456789abcdef)
static uint32_t seen;
uint32_t sl_abi(void) { return 2; }
uint64_t sl_create(const void *p) {
    return p && *(const uint8_t *)p == 0x42 ? HANDLE : 0;
}
void sl_destroy(uint64_t id) { if (id == HANDLE) seen |= 1; }
int32_t sl_input(uint64_t id, const void *p, uint32_t n) {
    if (id != HANDLE || n != 17 || !p || *(const uint8_t *)p != 0x42) return -99;
    seen |= 2; return -123;
}
void sl_tick(void) { seen |= 4; }
uint32_t sl_drain(uint64_t id, void *p, uint32_t n) {
    if (id != HANDLE || n != 17 || !p) return 0;
    *(uint8_t *)p = 0x51; seen |= 8; return 17;
}
uint32_t sl_output(uint64_t id, void *p, uint32_t n) {
    if (id != HANDLE || n != 17 || !p || *(uint8_t *)p != 0x51) return 0;
    seen |= 16; return 17;
}
int32_t sl_read(uint64_t id, void *p, uint32_t n, void *a) {
    if (id != HANDLE || n != 17 || !p || !a) return -99;
    *(uint8_t *)p = 0x61; *(uint8_t *)a = 0x71; seen |= 32; return 17;
}
int32_t sl_write(uint64_t id, const void *p, uint32_t n, const void *a) {
    if (id != HANDLE || n != 17 || !p || !a || *(const uint8_t *)p != 0x61 || *(const uint8_t *)a != 0x71) return -99;
    seen |= 64; return 17;
}
int32_t sl_shutdown(uint64_t id, uint32_t r, uint32_t w) {
    if (id != HANDLE || r != 0x1234 || w != 0x5678) return -99;
    seen |= 128; return -1234567;
}
void sl_close(uint64_t id, uint32_t abort) { if (id == HANDLE && abort == 0x9abc) seen |= 256; }
SL_API uint32_t sl_fixture_status(void) { return seen; }
