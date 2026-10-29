/* Exercise the allocator used by both the bridge and the unmodified lwIP core. */
#include "../bridge.c"
#include <assert.h>
#include <stdio.h>
#include <stddef.h>

int main(void) {
    assert(!sl_alloc(SIZE_MAX));
    assert(!sl_calloc(SIZE_MAX,2));
    sl_free(NULL);
    for (size_t n=1;n<=140000;n=n*2+1) {
        uint8_t *a=sl_alloc(n), *b=sl_alloc(n);
        assert(a && b && a!=b);
        assert((uintptr_t)a % _Alignof(max_align_t)==0);
        memset(a,0x5a,n); memset(b,0xa5,n);
        sl_free(b);
        b=sl_calloc(n,1);
        assert(b);
        for (size_t i=0;i<n;i++) { assert(a[i]==0x5a); assert(b[i]==0); }
        sl_free(a); sl_free(b);
    }
    void *held[1024];
    for (size_t i=0;i<1024;i++) {
        held[i]=sl_alloc(i&1?65535:65575);
        assert(held[i]);
    }
    for (size_t i=0;i<1024;i++) sl_free(held[i]);
    assert(alloc_retained>0 && alloc_retained<=SL_ALLOC_RETAIN);
    for (size_t i=0;i<SL_ALLOC_BINS;i++) assert(alloc_bin_bytes[i]<=SL_ALLOC_BIN_RETAIN);
    /* Recycled pbufs keep outstanding references and data until the last free. */
    lwip_init();
    struct pbuf *p=pbuf_alloc(PBUF_RAW,65535,PBUF_RAM);
    assert(p); memset(p->payload,0x63,p->len); pbuf_ref(p);
    assert(!pbuf_free(p));
    struct pbuf *other=pbuf_alloc(PBUF_RAW,65535,PBUF_RAM);
    assert(other && other!=p); memset(other->payload,0x17,other->len);
    for (size_t i=0;i<p->len;i++) assert(((uint8_t *)p->payload)[i]==0x63);
    assert(pbuf_free(other)==1 && pbuf_free(p)==1);
    lwip_fini(); clear_alloc();
    assert(!alloc_retained);
    for (size_t i=0;i<SL_ALLOC_BINS;i++) assert(!alloc_free[i] && !alloc_bin_bytes[i]);
    puts("native allocator alignment, overflow, ownership and bounds: PASS");
    return 0;
}
