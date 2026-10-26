//go:build cgo && !with_purego && !with_lwip_prebuilt

#include "bridge.h"
#include <stdlib.h>
#include <string.h>
#include <time.h>
#ifdef _WIN32
#include <windows.h>
#include <stdarg.h>
#include <stdio.h>
#endif
#include <lwip/init.h>
#include <lwip/netif.h>
#include <lwip/tcp.h>
#include <lwip/udp.h>
#include <lwip/nd6.h>
#include <lwip/ip4_frag.h>
#include <lwip/ip6_frag.h>
#include <lwip/priv/tcp_priv.h>

typedef struct sl_context sl_context;
typedef struct sl_chunk {
    struct sl_chunk *next;
    struct pbuf *p;
    uint32_t offset;
    uint8_t address[40];
} sl_chunk;
typedef struct sl_connection {
    struct sl_connection *hash_next, *dirty_next;
    sl_context *owner;
    uint64_t id;
    struct tcp_pcb *tcp;
    struct udp_pcb *udp;
    sl_chunk *head, *tail;
    uint32_t queued, packets;
    int dirty, announced, eof, failed, rx_closed, tx_closed;
    uint8_t address[40];
} sl_connection;
typedef struct sl_packet {
    struct sl_packet *next;
    uint32_t len;
    uint16_t gso_size;
    uint8_t data[];
} sl_packet;
struct sl_context {
    struct sl_context *next;
    uint64_t id;
    struct netif iface;
    struct tcp_pcb *listener;
    struct udp_pcb *udp_listener;
    sl_connection *dirty_head, *dirty_tail;
    sl_packet *out_head, *out_tail;
    uint32_t out_bytes, count, max_count;
    int added, tcp_gso;
};
static sl_context *contexts;
static sl_connection *connections[1024];
static uint64_t next_stack, next_connection;
static uint32_t ticks;

/* Large receive buffers outlive sl_input (queued TCP, out-of-order data or IP
 * fragments). Recycle only through lwIP's final pbuf release, never on return
 * from input. All access is serialized by the Go core, including teardown.
 * Keep at most 2 MiB of payload storage process-wide, rather than letting libc
 * repeatedly trim and fault in the same pages for concurrent GSO streams. */
#define SL_RX_RETAIN 32
typedef struct sl_rx_buffer {
    struct pbuf_custom custom;
    struct sl_rx_buffer *next;
    uint32_t data[16384];
} sl_rx_buffer;
static sl_rx_buffer *rx_free;
static unsigned rx_retained;

static void release_rx(struct pbuf *p) {
    sl_rx_buffer *b=(sl_rx_buffer *)p;
    if (rx_retained<SL_RX_RETAIN) {
        b->next=rx_free; rx_free=b; rx_retained++;
    } else free(b);
}
static struct pbuf *alloc_rx(u16_t len) {
    /* Pool only large packets; rounding up costs less than 2x active storage. */
    if (len<=32768) return pbuf_alloc(PBUF_RAW,len,PBUF_RAM);
    sl_rx_buffer *b=rx_free;
    if (b) { rx_free=b->next; rx_retained--; }
    else b=malloc(sizeof(*b));
    if (!b) return NULL;
    b->custom.custom_free_function=release_rx;
    return pbuf_alloced_custom(PBUF_RAW,len,PBUF_RAM,&b->custom,b->data,65535);
}
static void clear_rx(void) {
    while (rx_free) { sl_rx_buffer *b=rx_free; rx_free=b->next; free(b); }
    rx_retained=0;
}

#ifdef _WIN32
/* The embedded core does not link lwIP's standalone Win32 port executable. */
void lwip_win32_platform_diag(const char *format, ...) {
    va_list args;
    va_start(args,format);
    vfprintf(stderr,format,args);
    va_end(args);
}
#endif
unsigned int lwip_port_rand(void) { return (unsigned int)rand(); }
u32_t sys_now(void) {
#ifdef _WIN32
    return (u32_t)GetTickCount64();
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (u32_t)((uint64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000);
#endif
}
uint32_t sl_abi(void) { return 2; }
static uint32_t get32(const uint8_t *p) {
    return (uint32_t)p[0] | ((uint32_t)p[1]<<8) | ((uint32_t)p[2]<<16) | ((uint32_t)p[3]<<24);
}
static void put32(uint8_t *p, uint32_t n) {
    for (int i=0;i<4;i++) p[i]=(uint8_t)(n>>(8*i));
}
static void put64(uint8_t *p, uint64_t n) {
    for (int i=0;i<8;i++) p[i]=(uint8_t)(n>>(8*i));
}
static sl_context *context(uint64_t id) {
    for (sl_context *s=contexts;s;s=s->next) if (s->id==id) return s;
    return NULL;
}
static sl_connection *connection(uint64_t id) {
    for (sl_connection *c=connections[id%1024];c;c=c->hash_next) if (c->id==id) return c;
    return NULL;
}
static void notify(sl_connection *c) {
    if (c->dirty) return;
    sl_context *s=c->owner;
    c->dirty=1; c->dirty_next=NULL;
    if (s->dirty_tail) s->dirty_tail->dirty_next=c;
    else s->dirty_head=c;
    s->dirty_tail=c;
}
static void address_copy(uint8_t *p, const ip_addr_t *a) {
    memset(p,0,16);
    if (IP_IS_V6(a)) memcpy(p,ip_2_ip6(a)->addr,16);
    else memcpy(p,&ip_2_ip4(a)->addr,4);
}
static void address_make(uint8_t *p, const ip_addr_t *src, uint16_t sp,
                         const ip_addr_t *dst, uint16_t dp, uint8_t proto) {
    memset(p,0,40);
    address_copy(p,src); address_copy(p+16,dst);
    p[32]=sp; p[33]=sp>>8; p[34]=dp; p[35]=dp>>8;
    p[36]=IP_IS_V6(src)?6:4; p[37]=proto;
}
static sl_connection *new_connection(sl_context *s, const ip_addr_t *src, uint16_t sp,
                                     const ip_addr_t *dst, uint16_t dp, uint8_t proto) {
    if (s->count>=s->max_count) return NULL;
    sl_connection *c=calloc(1,sizeof(*c));
    if (!c) return NULL;
    c->id=++next_connection; c->owner=s; s->count++;
    address_make(c->address,src,sp,dst,dp,proto);
    c->hash_next=connections[c->id%1024]; connections[c->id%1024]=c;
    notify(c);
    return c;
}
static int enqueue(sl_connection *c, struct pbuf *p) {
    if (c->queued+p->tot_len>262144 || c->packets>=256) return 0;
    sl_chunk *q=calloc(1,sizeof(*q));
    if (!q) return 0;
    q->p=p;
    if (c->udp) address_make(q->address,&c->udp->remote_ip,c->udp->remote_port,
                             &c->udp->local_ip,c->udp->local_port,17);
    if (c->tail) c->tail->next=q; else c->head=q;
    c->tail=q; c->queued+=p->tot_len; c->packets++;
    notify(c); return 1;
}
static void discard_input(sl_connection *c) {
    while (c->head) {
        sl_chunk *q=c->head; c->head=q->next;
        if (c->tcp) tcp_recved(c->tcp,(u16_t)(q->p->tot_len-q->offset));
        pbuf_free(q->p); free(q);
    }
    c->tail=NULL; c->queued=0; c->packets=0;
}
static err_t on_recv(void *arg, struct tcp_pcb *pcb, struct pbuf *p, err_t err) {
    sl_connection *c=arg; (void)err;
    if (!p) { c->eof=1; notify(c); return ERR_OK; }
    if (c->rx_closed) { tcp_recved(pcb,p->tot_len); pbuf_free(p); return ERR_OK; }
    return enqueue(c,p)?ERR_OK:ERR_MEM;
}
static err_t on_sent(void *arg, struct tcp_pcb *pcb, u16_t len) {
    (void)pcb; (void)len; notify(arg); return ERR_OK;
}
static err_t on_poll(void *arg, struct tcp_pcb *pcb) {
    (void)pcb; notify(arg); return ERR_OK;
}
static void on_error(void *arg, err_t err) {
    sl_connection *c=arg;
    c->tcp=NULL; c->failed=err==ERR_RST?-3:-2; notify(c);
}
static void on_destroy(u8_t id, void *arg) {
    (void)id;
    sl_connection *c=arg;
    if (c) { c->tcp=NULL; notify(c); }
}
static const struct tcp_ext_arg_callbacks ext_callbacks={on_destroy,NULL};
static err_t on_accept(void *arg, struct tcp_pcb *pcb, err_t err) {
    if (err!=ERR_OK) return err;
    sl_context *s=arg;
    sl_connection *c=new_connection(s,&pcb->remote_ip,pcb->remote_port,&pcb->local_ip,pcb->local_port,6);
    if (!c) { tcp_abort(pcb); return ERR_ABRT; }
    c->tcp=pcb;
    /* Keep the negotiated wire MSS for congestion control and SYN options.
     * Leave room for all IP/TCP headers and the device's framing below 64 KiB.
     * Very small MSS peers retain ordinary segmentation. */
    if (s->tcp_gso && pcb->mss>=256
#if LWIP_TCP_TIMESTAMPS
        && !(pcb->flags & TF_TIMESTAMP)
#endif
    ) {
        pcb->gso_mss=pcb->mss;
        pcb->gso_max_size=65535-128;
    }
    tcp_arg(pcb,c); tcp_recv(pcb,on_recv); tcp_sent(pcb,on_sent); tcp_err(pcb,on_error);
    tcp_poll(pcb,on_poll,2);
    tcp_ext_arg_set(pcb,0,c); tcp_ext_arg_set_callbacks(pcb,0,&ext_callbacks);
    tcp_nagle_disable(pcb);
    return ERR_OK;
}
static void on_udp_recv(void *arg, struct udp_pcb *pcb, struct pbuf *p, const ip_addr_t *a, u16_t port) {
    (void)pcb; (void)a; (void)port;
    if (!enqueue(arg,p)) pbuf_free(p);
}
static void on_udp_accept(void *arg, struct udp_pcb *pcb, struct pbuf *p, const ip_addr_t *a, u16_t port) {
    (void)p;
    sl_connection *c=new_connection(arg,&pcb->remote_ip,pcb->remote_port,a,port,17);
    if (!c) { udp_remove(pcb); return; }
    c->udp=pcb; udp_recv(pcb,on_udp_recv,c);
}
static err_t output(struct netif *n, struct pbuf *p) {
    sl_context *s=n->state;
    if (s->out_bytes+p->tot_len>(4u<<20)) return ERR_MEM;
    sl_packet *q=malloc(sizeof(*q)+p->tot_len);
    if (!q) return ERR_MEM;
    q->len=p->tot_len; q->gso_size=p->gso_size; q->next=NULL;
    pbuf_copy_partial(p,q->data,p->tot_len,0);
    if (s->out_tail) s->out_tail->next=q; else s->out_head=q;
    s->out_tail=q; s->out_bytes+=q->len;
    return ERR_OK;
}
static err_t output4(struct netif *n, struct pbuf *p, const ip4_addr_t *a) { (void)a; return output(n,p); }
static err_t output6(struct netif *n, struct pbuf *p, const ip6_addr_t *a) { (void)a; return output(n,p); }
static err_t init_if(struct netif *n) { n->output=output4; n->output_ip6=output6; return ERR_OK; }

uint64_t sl_create(const void *config) {
    const uint8_t *cfg=config;
    uint32_t mtu=get32(cfg);
    if (mtu<576 || mtu>65535 || (get32(cfg+12)&~1u)) return 0;
    sl_context *s=calloc(1,sizeof(*s));
    if (!s) return 0;
    if (!contexts) { lwip_init(); ticks=0; }
    s->id=++next_stack; s->max_count=get32(cfg+4);
    s->tcp_gso=(get32(cfg+12)&1)!=0;
    if (!s->max_count) s->max_count=16384;
    s->next=contexts; contexts=s;
    if (!netif_add_noaddr(&s->iface,s,init_if,ip_input)) goto fail;
    s->added=1;
    s->iface.mtu=(u16_t)mtu;
#if LWIP_IPV6 && LWIP_ND6_ALLOW_RA_UPDATES
    /* netif_add initializes mtu6 before the embedding assigns the real MTU. */
    s->iface.mtu6=(u16_t)mtu;
#endif
    ip4_addr_t a4,mask,gw;
    memcpy(&a4.addr,cfg+8,4); ip4_addr_set_zero(&mask); ip4_addr_set_zero(&gw);
    netif_set_addr(&s->iface,&a4,&mask,&gw);
    ip6_addr_t a6; memset(&a6,0,sizeof(a6)); memcpy(a6.addr,cfg+16,16);
    netif_add_ip6_address(&s->iface,&a6,NULL);
    netif_set_up(&s->iface); netif_set_link_up(&s->iface); netif_set_default(&s->iface);
    netif_set_flags(&s->iface,NETIF_FLAG_PRETEND_TCP|NETIF_FLAG_PRETEND_UDP|NETIF_FLAG_PRETEND_ICMP);
    s->listener=tcp_new_ip_type(IPADDR_TYPE_ANY);
    if (!s->listener) goto fail;
    tcp_bind_netif(s->listener,&s->iface);
    if (tcp_bind(s->listener,NULL,0)!=ERR_OK) goto fail;
    struct tcp_pcb *listener=tcp_listen(s->listener);
    if (!listener) goto fail;
    s->listener=listener; tcp_arg(listener,s); tcp_accept(listener,on_accept);
    s->udp_listener=udp_new_ip_type(IPADDR_TYPE_ANY);
    if (!s->udp_listener) goto fail;
    udp_bind_netif(s->udp_listener,&s->iface);
    if (udp_bind(s->udp_listener,NULL,0)!=ERR_OK) goto fail;
    udp_recv(s->udp_listener,on_udp_accept,s);
    return s->id;
fail:
    sl_destroy(s->id); return 0;
}
int32_t sl_input(uint64_t id, const void *data, uint32_t len) {
    sl_context *s=context(id);
    if (!s) return -2;
    if (len<20 || len>65535) return -4;
    struct pbuf *p=alloc_rx((u16_t)len);
    if (!p) return -5;
    pbuf_take(p,data,(u16_t)len);
    err_t e=s->iface.input(p,&s->iface);
    if (e!=ERR_OK) { pbuf_free(p); return -4; }
    return 0;
}
void sl_tick(void) {
    if (!contexts) return;
    tcp_tmr();
    if (++ticks%4==0) { ip_reass_tmr(); nd6_tmr(); ip6_reass_tmr(); }
}
uint32_t sl_output(uint64_t id, void *data, uint32_t cap) {
    sl_context *s=context(id);
    if (!s) return 0;
    uint8_t *p=data; uint32_t used=0;
    while (s->out_head && used+8+s->out_head->len<=cap) {
        sl_packet *q=s->out_head;
        put32(p+used,q->len);
        put32(p+used+4,q->gso_size);
        memcpy(p+used+8,q->data,q->len); used+=8+q->len;
        s->out_bytes-=q->len; s->out_head=q->next;
        if (!s->out_head) s->out_tail=NULL;
        free(q);
    }
    return used;
}
uint32_t sl_drain(uint64_t id, void *data, uint32_t cap) {
    sl_context *s=context(id);
    if (!s) return 0;
    uint8_t *p=data; uint32_t used=0;
    while (s->dirty_head && used+56<=cap) {
        sl_connection *c=s->dirty_head;
        uint32_t len=c->announced?0:40;
        put32(p+used,c->announced?3:(c->udp?2:1)); put32(p+used+4,len); put64(p+used+8,c->id);
        if (len) memcpy(p+used+16,c->address,len);
        used+=16+len; c->announced=1; c->dirty=0;
        s->dirty_head=c->dirty_next; if (!s->dirty_head) s->dirty_tail=NULL;
        c->dirty_next=NULL;
    }
    return used;
}
int32_t sl_read(uint64_t id, void *data, uint32_t cap, void *address) {
    sl_connection *c=connection(id);
    if (!c) return -2;
    if (c->rx_closed) return -6;
    if (cap>65535) cap=65535;
    uint32_t used=0; int datagram=c->udp && c->head;
    while (c->head && (used<cap || datagram)) {
        sl_chunk *q=c->head;
        uint32_t n=q->p->tot_len-q->offset;
        if (n>cap-used) n=cap-used;
        if (c->udp && address) memcpy(address,q->address,40);
        if (n) pbuf_copy_partial(q->p,(uint8_t *)data+used,(u16_t)n,(u16_t)q->offset);
        q->offset+=n; used+=n; c->queued-=n;
        if (q->offset==q->p->tot_len || c->udp) {
            c->queued-=q->p->tot_len-q->offset; c->packets--;
            c->head=q->next; if (!c->head) c->tail=NULL;
            pbuf_free(q->p); free(q);
        }
        if (c->udp) break;
    }
    if (used || datagram) {
        if (c->tcp) { tcp_recved(c->tcp,(u16_t)used); tcp_output(c->tcp); }
        return (int32_t)used;
    }
    if (c->failed) return c->failed;
    return c->eof?0:-1;
}
int32_t sl_write(uint64_t id, const void *data, uint32_t len, const void *address) {
    sl_connection *c=connection(id);
    if (!c || c->tx_closed) return -2;
    if (c->failed) return c->failed;
    if (c->udp) {
        const uint8_t *a=address;
        if (!a || len>65507 || a[36]!=c->address[36]) return -4;
        ip_addr_t src;
        memset(&src,0,sizeof(src));
        if (a[36]==6) { IP_SET_TYPE_VAL(src,IPADDR_TYPE_V6); memcpy(ip_2_ip6(&src)->addr,a+16,16); }
        else { IP_SET_TYPE_VAL(src,IPADDR_TYPE_V4); memcpy(&ip_2_ip4(&src)->addr,a+16,4); }
        uint16_t port=(uint16_t)a[34]|((uint16_t)a[35]<<8);
        struct pbuf *p=pbuf_alloc(PBUF_TRANSPORT,(u16_t)len,PBUF_RAM);
        if (!p) return -5;
        if (len) pbuf_take(p,data,(u16_t)len);
        err_t e=udp_sendfrom(c->udp,p,&src,port); pbuf_free(p);
        return e==ERR_OK?(int32_t)len:(e==ERR_MEM?-5:-4);
    }
    if (!c->tcp) return -2;
    uint32_t n=tcp_sndbuf(c->tcp);
    if (n>len) n=len;
    if (n>65535) n=65535;
    if (!n) return -1;
    err_t e=tcp_write(c->tcp,data,(u16_t)n,TCP_WRITE_FLAG_COPY);
    if (e==ERR_MEM) return -1;
    if (e!=ERR_OK) return -2;
    tcp_output(c->tcp); return (int32_t)n;
}
int32_t sl_shutdown(uint64_t id, uint32_t read, uint32_t write) {
    sl_connection *c=connection(id);
    if (!c || c->udp) return -2;
    if (read) { discard_input(c); c->rx_closed=1; }
    if (write && !c->tx_closed && c->tcp) {
        err_t e=tcp_shutdown(c->tcp,0,1);
        if (e==ERR_MEM) return -1;
        if (e!=ERR_OK) return -2;
        c->tx_closed=1;
    }
    notify(c); return 0;
}
void sl_close(uint64_t id, uint32_t abort) {
    sl_connection *c=connection(id);
    if (!c) return;
    sl_context *s=c->owner;
    if (c->tcp) {
        struct tcp_pcb *pcb=c->tcp;
        tcp_ext_arg_set(pcb,0,NULL);
        tcp_arg(pcb,NULL); tcp_recv(pcb,NULL); tcp_sent(pcb,NULL); tcp_err(pcb,NULL); tcp_poll(pcb,NULL,0);
        c->tcp=NULL;
        if (abort || tcp_close(pcb)!=ERR_OK) tcp_abort(pcb);
    }
    if (c->udp) udp_remove(c->udp);
    discard_input(c);
    sl_connection **hp=&connections[id%1024];
    while (*hp!=c) hp=&(*hp)->hash_next;
    *hp=c->hash_next;
    sl_connection **dp=&s->dirty_head,*prev=NULL;
    while (*dp && *dp!=c) { prev=*dp; dp=&(*dp)->dirty_next; }
    if (*dp) { *dp=c->dirty_next; if (s->dirty_tail==c) s->dirty_tail=prev; }
    s->count--; free(c);
}
void sl_destroy(uint64_t id) {
    sl_context *s=context(id);
    if (!s) return;
    for (int i=0;i<1024;i++) {
        sl_connection *c=connections[i];
        while (c) { sl_connection *next=c->hash_next; if (c->owner==s) sl_close(c->id,1); c=next; }
    }
    if (s->listener) tcp_close(s->listener);
    if (s->udp_listener) udp_remove(s->udp_listener);
    /* Also remove pending handshakes and TIME_WAIT PCBs owned by this netif. */
    if (s->added) {
        struct tcp_pcb **lists[]={&tcp_active_pcbs,&tcp_bound_pcbs,&tcp_tw_pcbs};
        for (unsigned i=0;i<sizeof(lists)/sizeof(lists[0]);i++) {
            struct tcp_pcb *p=*lists[i];
            while (p) { struct tcp_pcb *next=p->next; if (p->netif_idx==netif_get_index(&s->iface)) tcp_abort(p); p=next; }
        }
        ip_reass_remove_netif(&s->iface);
        ip6_reass_remove_netif(&s->iface);
        netif_remove(&s->iface);
    }
    sl_context **pp=&contexts;
    while (*pp!=s) pp=&(*pp)->next;
    *pp=s->next;
    while (s->out_head) { sl_packet *p=s->out_head; s->out_head=p->next; free(p); }
    free(s);
    if (!contexts) { lwip_fini(); clear_rx(); }
    else netif_set_default(&contexts->iface);
}
