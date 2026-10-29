/* Byte-wise oracle and guard pages: sanitizers cannot instrument assembly loads.
 * Also exercise odd pbuf splits through lwIP's real chained/pseudo checksums. */
#include "../checksum.h"
#include <lwip/inet_chksum.h>
#include <lwip/init.h>
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#ifdef SING_LWIP_STANDALONE
unsigned int lwip_port_rand(void) { return (unsigned int)rand(); }
#endif
#ifdef _WIN32
#include <windows.h>
#else
#include <sys/mman.h>
#include <unistd.h>
#endif

static uint16_t oracle(const uint8_t *p, unsigned n) {
    uint64_t sum = 0;
    for (unsigned i = 0; i < n; i++) sum += (uint64_t)p[i] << ((i & 1) ? 0 : 8);
    while (sum >> 16) sum = (sum & 65535) + (sum >> 16);
    uint8_t bytes[] = {(uint8_t)(sum >> 8), (uint8_t)sum};
    uint16_t result;
    memcpy(&result, bytes, 2);
    return result;
}
static void check(const uint8_t *p, unsigned n) {
    uint16_t want = oracle(p, n), got = sl_checksum(p, (int)n);
    if (got != want) {
        fprintf(stderr, "checksum length=%u offset=%u got=%04x want=%04x\n",
                n, (unsigned)((uintptr_t)p & 63), got, want);
        abort();
    }
    if (n <= 65535) assert(inet_chksum(p, (u16_t)n) == (uint16_t)~want);
}
static void boundaries(void) {
    static const unsigned sizes[] = {1023,1024,1025,1499,1500,1501,8191,8192,9000,
        16383,16384,32767,32768,65495,65534,65535,65536,65537,131071,131072,131073,1048577};
    uint8_t *data = malloc(1048577+64);
    assert(data);
    for (unsigned pattern = 0; pattern < 3; pattern++) {
        uint32_t state = 0x137bad;
        for (unsigned i = 0; i < 1048577+64; i++) {
            state ^= state << 13; state ^= state >> 17; state ^= state << 5;
            data[i] = pattern == 0 ? (uint8_t)state : pattern == 1 ? 255 : 0;
        }
        for (unsigned offset = 0; offset < 64; offset++) {
            for (unsigned n = 0; n <= 512; n++) check(data+offset, n);
            for (unsigned i = 0; i < sizeof(sizes)/sizeof(*sizes); i++) check(data+offset,sizes[i]);
        }
    }
    check(NULL, 0);
    free(data);
}
static void guard_pages(void) {
#ifdef _WIN32
    SYSTEM_INFO info;
    GetSystemInfo(&info);
    size_t page = info.dwPageSize;
    size_t size = (65536+page-1)/page*page;
    uint8_t *base = VirtualAlloc(NULL, size+2*page, MEM_COMMIT|MEM_RESERVE, PAGE_READWRITE);
    assert(base);
    DWORD old;
    assert(VirtualProtect(base,page,PAGE_NOACCESS,&old));
    assert(VirtualProtect(base+page+size,page,PAGE_NOACCESS,&old));
#else
    size_t page = (size_t)sysconf(_SC_PAGESIZE);
    size_t size = (65536+page-1)/page*page;
    uint8_t *base = mmap(NULL,size+2*page,PROT_READ|PROT_WRITE,MAP_PRIVATE|MAP_ANONYMOUS,-1,0);
    assert(base != MAP_FAILED);
    assert(!mprotect(base,page,PROT_NONE));
    assert(!mprotect(base+page+size,page,PROT_NONE));
#endif
    uint8_t *data = base+page;
    for (unsigned i = 0; i < size; i++) data[i] = (uint8_t)(i*31+7);
    for (unsigned n = 0; n <= 512; n++) { check(data,n); check(data+size-n,n); }
    for (unsigned n = 65472; n <= 65536; n++) { check(data,n); check(data+size-n,n); }
#ifdef _WIN32
    assert(VirtualFree(base,0,MEM_RELEASE));
#else
    assert(!munmap(base,size+2*page));
#endif
}
static void chained(void) {
    uint8_t data[9217], pseudo[40+sizeof(data)];
    for (unsigned i = 0; i < sizeof(data); i++) data[i] = (uint8_t)(i*37+11);
    const unsigned chunks[] = {1,63,65,1501,7587};
    struct pbuf *chain = NULL;
    unsigned offset = 0;
    lwip_init();
    for (unsigned i = 0; i < sizeof(chunks)/sizeof(*chunks); i++) {
        struct pbuf *p = pbuf_alloc(PBUF_RAW,(u16_t)chunks[i],PBUF_RAM);
        assert(p); assert(pbuf_take(p,data+offset,(u16_t)chunks[i])==ERR_OK);
        offset += chunks[i];
        if (chain) pbuf_cat(chain,p); else chain = p;
    }
    assert(offset == sizeof(data));
    assert(inet_chksum_pbuf(chain) == (uint16_t)~oracle(data,sizeof(data)));
    ip4_addr_t src4, dst4;
    const uint8_t addr4[] = {198,18,0,10,198,18,0,20};
    memcpy(&src4.addr,addr4,4); memcpy(&dst4.addr,addr4+4,4);
    memcpy(pseudo,addr4,8); pseudo[8]=0; pseudo[9]=6;
    pseudo[10]=sizeof(data)>>8; pseudo[11]=(uint8_t)sizeof(data);
    memcpy(pseudo+12,data,sizeof(data));
    assert(inet_chksum_pseudo(chain,6,sizeof(data),&src4,&dst4)==(uint16_t)~oracle(pseudo,12+sizeof(data)));
    ip6_addr_t src6={0}, dst6={0};
    memset(pseudo,0,40); pseudo[0]=0xfd; pseudo[15]=10; pseudo[16]=0xfd; pseudo[31]=20;
    memcpy(src6.addr,pseudo,16); memcpy(dst6.addr,pseudo+16,16);
    pseudo[34]=sizeof(data)>>8; pseudo[35]=(uint8_t)sizeof(data); pseudo[39]=6;
    memcpy(pseudo+40,data,sizeof(data));
    assert(ip6_chksum_pseudo(chain,6,sizeof(data),&src6,&dst6)==(uint16_t)~oracle(pseudo,40+sizeof(data)));
    pbuf_free(chain);
    lwip_fini();
}
static void copy_cases(void) {
    static const unsigned sizes[] = {0,1,2,3,19,20,40,63,64,65,127,128,129,255,256,257,
        511,512,513,1499,1500,8191,8192,9000,32767,32768,65495,65534,65535};
    uint8_t *src=malloc(65535+64), *dst=malloc(65535+128);
    assert(src && dst);
    for (unsigned pattern=0;pattern<3;pattern++) {
        for (unsigned i=0;i<65535+64;i++) src[i]=pattern==0?(uint8_t)(i*37+i/251):pattern==1?255:0;
        for (unsigned a=0;a<64;a++) {
            for (unsigned b=0;b<64;b++) {
                for (unsigned i=0;i<sizeof(sizes)/sizeof(*sizes);i++) {
                    unsigned n=sizes[i];
                    if (n>512 && b!=(a*17+3)%64) continue;
                    memset(dst,0xa5,n+b+2);
                    uint16_t sum=LWIP_CHKSUM_COPY(dst+b+1,src+a,(u16_t)n);
                    assert(sum==oracle(src+a,n));
                    assert(!memcmp(dst+b+1,src+a,n));
                    assert(dst[b]==0xa5 && dst[b+1+n]==0xa5);
                }
            }
        }
    }
    assert(sl_checksum_copy_impl(NULL,NULL,0)==0);
    free(src); free(dst);
}
static void copy_guards(void) {
#ifdef _WIN32
    SYSTEM_INFO info; GetSystemInfo(&info);
    size_t page=info.dwPageSize, size=(65536+page-1)/page*page;
    uint8_t *src=VirtualAlloc(NULL,size+2*page,MEM_COMMIT|MEM_RESERVE,PAGE_READWRITE);
    uint8_t *dst=VirtualAlloc(NULL,size+2*page,MEM_COMMIT|MEM_RESERVE,PAGE_READWRITE);
    assert(src && dst);
    DWORD old;
    assert(VirtualProtect(src,page,PAGE_NOACCESS,&old));
    assert(VirtualProtect(src+page+size,page,PAGE_NOACCESS,&old));
    assert(VirtualProtect(dst,page,PAGE_NOACCESS,&old));
    assert(VirtualProtect(dst+page+size,page,PAGE_NOACCESS,&old));
#else
    size_t page=(size_t)sysconf(_SC_PAGESIZE), size=(65536+page-1)/page*page;
    uint8_t *src=mmap(NULL,size+2*page,PROT_READ|PROT_WRITE,MAP_PRIVATE|MAP_ANONYMOUS,-1,0);
    uint8_t *dst=mmap(NULL,size+2*page,PROT_READ|PROT_WRITE,MAP_PRIVATE|MAP_ANONYMOUS,-1,0);
    assert(src!=MAP_FAILED && dst!=MAP_FAILED);
    assert(!mprotect(src,page,PROT_NONE) && !mprotect(src+page+size,page,PROT_NONE));
    assert(!mprotect(dst,page,PROT_NONE) && !mprotect(dst+page+size,page,PROT_NONE));
#endif
    for (unsigned i=0;i<size;i++) src[page+i]=(uint8_t)(i*31+17);
    /* Make source read-only too: an incorrect store operand must fault. */
#ifdef _WIN32
    assert(VirtualProtect(src+page,size,PAGE_READONLY,&old));
#else
    assert(!mprotect(src+page,size,PROT_READ));
#endif
    for (unsigned n=0;n<=65535;n++) {
        if (n>512 && n<65472) continue;
        for (unsigned ends=0;ends<4;ends++) {
            const uint8_t *s=src+page+((ends&1)?size-n:0);
            uint8_t *d=dst+page+((ends&2)?size-n:0);
            assert(LWIP_CHKSUM_COPY(d,s,(u16_t)n)==oracle(s,n));
            assert(!memcmp(d,s,n));
        }
    }
#ifdef _WIN32
    assert(VirtualFree(src,0,MEM_RELEASE) && VirtualFree(dst,0,MEM_RELEASE));
#else
    assert(!munmap(src,size+2*page) && !munmap(dst,size+2*page));
#endif
}
int main(void) {
    boundaries(); guard_pages(); chained(); copy_cases(); copy_guards();
    puts("checksum copy: source/destination alignments, contents, canaries and guards PASS");
#ifdef SL_CHECKSUM_BLOCKS
    puts("checksum " SL_CHECKSUM_BLOCKS ": oracle, alignments, overflow, guards, IPv4/IPv6 pbuf chains PASS");
#else
    puts("checksum scalar: oracle, alignments, overflow, guards, IPv4/IPv6 pbuf chains PASS");
#endif
    return 0;
}
