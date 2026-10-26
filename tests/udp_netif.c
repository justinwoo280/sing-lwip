/* Standalone HEV lwIP regression: no sing-lwip bridge, Go, sockets or TUN.
 * Two bound pretend listeners receive the same client endpoint on two netifs. */
#include <lwip/init.h>
#include <lwip/netif.h>
#include <lwip/udp.h>
#include <lwip/inet_chksum.h>
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <stdarg.h>
void lwip_win32_platform_diag(const char *format, ...) {
    va_list args; va_start(args,format); vfprintf(stderr,format,args); va_end(args);
}
#endif

unsigned int lwip_port_rand(void) { return (unsigned int)rand(); }
u32_t sys_now(void) { return 0; }

static unsigned accepted[2], received[2], mismatches;
static struct netif interfaces[2];
static unsigned tags[2]={0,1};
static err_t output4(struct netif *n, struct pbuf *p, const ip4_addr_t *a) {
    (void)n; (void)p; (void)a; return ERR_OK;
}
static err_t output6(struct netif *n, struct pbuf *p, const ip6_addr_t *a) {
    (void)n; (void)p; (void)a; return ERR_OK;
}
static err_t init_if(struct netif *n) {
    n->mtu=1500; n->output=output4; n->output_ip6=output6; return ERR_OK;
}
static void receive(void *arg, struct udp_pcb *pcb, struct pbuf *p, const ip_addr_t *a, u16_t port) {
    unsigned tag=*(unsigned *)arg;
    uint8_t value=0;
    (void)a;
    assert(p && p->tot_len==1 && port==40000);
    pbuf_copy_partial(p,&value,1,0);
    if (value!=tag || pcb->pretend_netif_idx!=netif_get_index(ip_current_input_netif())) mismatches++;
    received[tag]++;
    pbuf_free(p);
}
static void accept_peer(void *arg, struct udp_pcb *pcb, struct pbuf *p, const ip_addr_t *a, u16_t port) {
    (void)p; (void)a; (void)port;
    accepted[*(unsigned *)arg]++;
    udp_recv(pcb,receive,arg); /* udp_input dispatches the same pbuf after accept */
}
static void input(unsigned tag, int v6) {
    uint8_t bytes[49]={0};
    unsigned iplen=v6?40:20, len=iplen+9;
    if (v6) {
        bytes[0]=0x60; bytes[5]=9; bytes[6]=17; bytes[7]=64;
        bytes[8]=0xfd; bytes[23]=10; bytes[24]=0xfd; bytes[39]=20;
    } else {
        bytes[0]=0x45; bytes[3]=(uint8_t)len; bytes[8]=64; bytes[9]=17;
        bytes[12]=198; bytes[13]=18; bytes[15]=10;
        bytes[16]=198; bytes[17]=18; bytes[19]=20;
        uint16_t sum=inet_chksum(bytes,20); memcpy(bytes+10,&sum,2);
    }
    uint8_t *u=bytes+iplen;
    u[0]=0x9c; u[1]=0x40; u[2]=0x04; u[3]=0xd2; u[5]=9; u[8]=(uint8_t)tag;
    struct pbuf *payload=pbuf_alloc(PBUF_RAW,9,PBUF_RAM);
    assert(payload); pbuf_take(payload,u,9);
    uint16_t sum;
    if (v6) {
        ip6_addr_t src={0},dst={0}; memcpy(src.addr,bytes+8,16); memcpy(dst.addr,bytes+24,16);
        sum=ip6_chksum_pseudo(payload,17,9,&src,&dst);
    } else {
        ip4_addr_t src,dst; memcpy(&src.addr,bytes+12,4); memcpy(&dst.addr,bytes+16,4);
        sum=inet_chksum_pseudo(payload,17,9,&src,&dst);
    }
    if (!sum) sum=0xffff;
    memcpy(u+6,&sum,2); pbuf_free(payload);
    struct pbuf *p=pbuf_alloc(PBUF_RAW,(u16_t)len,PBUF_RAM);
    assert(p); pbuf_take(p,bytes,(u16_t)len);
    assert(ip_input(p,&interfaces[tag])==ERR_OK);
}
static unsigned run(int v6) {
    lwip_init();
    memset(accepted,0,sizeof(accepted)); memset(received,0,sizeof(received)); mismatches=0;
    memset(interfaces,0,sizeof(interfaces));
    for (unsigned i=0;i<2;i++) {
        struct netif *n=&interfaces[i];
        assert(netif_add_noaddr(n,NULL,init_if,ip_input));
        ip4_addr_t a4,mask,gw; IP4_ADDR(&a4,198,18,0,1); IP4_ADDR(&mask,255,255,255,0); ip4_addr_set_zero(&gw);
        netif_set_addr(n,&a4,&mask,&gw);
        ip6_addr_t a6; assert(ip6addr_aton("fd00::1",&a6)); netif_add_ip6_address(n,&a6,NULL);
        netif_set_up(n); netif_set_link_up(n); netif_set_flags(n,NETIF_FLAG_PRETEND_UDP);
        struct udp_pcb *pcb=udp_new_ip_type(IPADDR_TYPE_ANY);
        assert(pcb); udp_bind_netif(pcb,n); assert(udp_bind(pcb,NULL,0)==ERR_OK);
        udp_recv(pcb,accept_peer,&tags[i]);
    }
    for (unsigned i=0;i<4;i++) { input(0,v6); input(1,v6); }
    printf("IPv%d: mismatches=%u accepts=%u/%u receives=%u/%u\n",v6?6:4,mismatches,accepted[0],accepted[1],received[0],received[1]);
    unsigned failed=mismatches || accepted[0]!=1 || accepted[1]!=1 || received[0]!=4 || received[1]!=4;
    while (udp_pcbs) udp_remove(udp_pcbs);
    netif_remove(&interfaces[0]); netif_remove(&interfaces[1]);
    lwip_fini();
    return failed;
}
int main(void) {
    unsigned failed=run(0); failed+=run(1);
    return failed?1:0;
}
