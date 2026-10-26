/* Real in-memory handshakes plus fault injection into output queue accounting.
 * Including the bridge keeps the wire-MSS/window invariants out of the ABI. */
#include "../bridge.c"
#include <lwip/inet_chksum.h>
#include <assert.h>
#include <stdio.h>

static void be16(uint8_t *p, uint16_t n) { p[0]=n>>8; p[1]=(uint8_t)n; }
static void be32(uint8_t *p, uint32_t n) { be16(p,(uint16_t)(n>>16)); be16(p+2,(uint16_t)n); }
static uint32_t read_be32(const uint8_t *p) { return ((uint32_t)p[0]<<24)|((uint32_t)p[1]<<16)|((uint32_t)p[2]<<8)|p[3]; }

static void input_tcp(uint64_t id, int v6, uint32_t ack, uint16_t wnd, int syn) {
    uint8_t packet[80]={0};
    unsigned iplen=v6?40:20, tcplen=syn?24:20;
    if (v6) {
        packet[0]=0x60; packet[6]=6; packet[7]=64; be16(packet+4,(uint16_t)tcplen);
        packet[8]=0xfd; packet[23]=10; packet[24]=0xfd; packet[39]=20;
    } else {
        packet[0]=0x45; packet[8]=64; packet[9]=6; be16(packet+2,(uint16_t)(iplen+tcplen));
        packet[12]=198; packet[13]=18; packet[15]=10;
        packet[16]=198; packet[17]=18; packet[19]=20;
        uint16_t sum=inet_chksum(packet,20); memcpy(packet+10,&sum,2);
    }
    uint8_t *tcp=packet+iplen;
    be16(tcp,40001); be16(tcp+2,80); be32(tcp+4,syn?100:101); be32(tcp+8,ack);
    tcp[12]=(uint8_t)((tcplen/4)<<4); tcp[13]=syn?TCP_SYN:TCP_ACK; be16(tcp+14,wnd);
    if (syn) { tcp[20]=2; tcp[21]=4; be16(tcp+22,1200); }
    struct pbuf *p=pbuf_alloc(PBUF_RAW,(u16_t)tcplen,PBUF_RAM);
    assert(p); pbuf_take(p,tcp,(u16_t)tcplen);
    uint16_t sum;
    if (v6) {
        ip6_addr_t src={0},dst={0}; memcpy(src.addr,packet+8,16); memcpy(dst.addr,packet+24,16);
        sum=ip6_chksum_pseudo(p,6,tcplen,&src,&dst);
    } else {
        ip4_addr_t src,dst; memcpy(&src.addr,packet+12,4); memcpy(&dst.addr,packet+16,4);
        sum=inet_chksum_pseudo(p,6,(u16_t)tcplen,&src,&dst);
    }
    pbuf_free(p); memcpy(tcp+16,&sum,2);
    assert(sl_input(id,packet,iplen+tcplen)==0);
}

static void drain(sl_context *s) {
    uint8_t *buffer=malloc(65543);
    assert(buffer);
    while (s->out_head) {
        uint32_t n=sl_output(s->id,buffer,65543);
        assert(n);
        for (uint32_t offset=0;offset<n;) {
            uint32_t len=get32(buffer+offset),gso=get32(buffer+offset+4);
            assert(len>=20 && len<=65535 && offset+8+len<=n);
            assert(gso==0 || gso==1200);
            offset+=8+len;
        }
    }
    free(buffer);
    assert(!s->out_bytes);
}

static sl_connection *connect_peer(int v6, int enabled, uint32_t *server) {
    uint8_t cfg[32]={0};
    put32(cfg,1500); put32(cfg+12,(uint32_t)enabled); cfg[8]=127; cfg[11]=1; cfg[31]=1;
    uint64_t id=sl_create(cfg);
    assert(id && sl_abi()==2);
    sl_context *s=context(id);
    input_tcp(id,v6,0,65535,1);
    assert(s->out_head && !s->out_head->gso_size);
    *server=read_be32(s->out_head->data+(v6?40:20)+4)+1;
    drain(s);
    input_tcp(id,v6,*server,65535,0);
    assert(s->count==1);
    sl_connection *c=NULL;
    for (unsigned i=0;i<1024;i++) for (sl_connection *p=connections[i];p;p=p->hash_next) if (p->owner==s) c=p;
    assert(c && c->tcp && c->tcp->mss==1200);
    assert(c->tcp->cwnd==4380); /* aggregation must not inflate initial cwnd */
    assert(c->tcp->gso_mss==(enabled?1200:0));
    drain(s);
    return c;
}

static void test_partial_ack_rto(int v6) {
    uint32_t server;
    sl_connection *c=connect_peer(v6,1,&server);
    sl_context *s=c->owner;
    unsigned iplen=v6?40:20;
    uint8_t data[30000]; memset(data,0x59,sizeof(data));
    assert(sl_write(c->id,data,sizeof(data),NULL)==sizeof(data));
    assert(c->tcp->mss==1200 && c->tcp->cwnd==4380);
    assert(s->out_head && s->out_head->gso_size==1200 && s->out_head->len>1500);
    assert(!s->out_head->next);
    assert(s->out_head->len==iplen+20+3600);
    uint8_t short_buffer[100];
    assert(sl_output(s->id,short_buffer,sizeof(short_buffer))==0 && s->out_head);
    drain(s);
    /* Keep an ACKed prefix inside the unacked aggregate, shrink the window,
     * and force the real retransmission timer to fire. */
    input_tcp(s->id,v6,server+1200,1800,0);
    assert(c->tcp->lastack==server+1200 && c->tcp->snd_wnd==1800);
    assert(!s->out_head);
    c->tcp->rtime=c->tcp->rto-1;
    tcp_slowtmr();
    assert(c->tcp->mss==1200 && c->tcp->cwnd==1200 && c->tcp->nrtx==1);
    assert(s->out_head);
    for (sl_packet *p=s->out_head;p;p=p->next) {
        uint32_t seq=read_be32(p->data+iplen+4),len=p->len-iplen-20;
        assert(seq+len<=c->tcp->lastack+c->tcp->cwnd);
        assert(seq+len>c->tcp->lastack);
        for (uint32_t i=0;i<len;i++) assert(p->data[iplen+20+i]==0x59);
    }
    drain(s);
    sl_destroy(s->id);
}

static void test_output_failure(int v6) {
    uint32_t server;
    sl_connection *c=connect_peer(v6,1,&server);
    sl_context *s=c->owner;
    uint8_t data[10000]; memset(data,0x87,sizeof(data));
    /* Only a wire-sized packet fits. The native sender must split and back
     * off GSO instead of permanently retrying the rejected aggregate. */
    s->out_bytes=(4u<<20)-1600;
    assert(sl_write(c->id,data,sizeof(data),NULL)==sizeof(data));
    assert(c->tcp->mss==1200 && c->tcp->gso_off_timer && c->tcp->gso_off_count);
    assert(s->out_head && s->out_head->len<=1500);
    s->out_bytes=0;
    for (sl_packet *p=s->out_head;p;p=p->next) s->out_bytes+=p->len;
    drain(s);
    input_tcp(s->id,v6,server+1200,65535,0);
    assert(s->out_head);
    for (sl_packet *p=s->out_head;p;p=p->next) assert(p->len<=1500);
    drain(s);
    sl_destroy(s->id);
}

static void test_max_aggregate(int v6) {
    uint32_t server;
    sl_connection *c=connect_peer(v6,1,&server);
    sl_context *s=c->owner;
    c->tcp->snd_wnd=c->tcp->snd_wnd_max=c->tcp->cwnd=131072;
    uint8_t *data=malloc(65535);
    assert(data);
    for (unsigned i=0;i<65535;i++) data[i]=(uint8_t)(i%251);
    assert(sl_write(c->id,data,65535,NULL)==65535);
    uint32_t seen=0;
    for (sl_packet *p=s->out_head;p;p=p->next) {
        unsigned hlen=(v6?40:20)+20;
        assert(p->gso_size==1200 && p->len<=65535);
        uint32_t payload=p->len-hlen;
        assert(payload<=65535-128 && seen+payload<=65535);
        assert(read_be32(p->data+hlen-16)==server+seen);
        assert(!memcmp(p->data+hlen,data+seen,payload));
        seen+=payload;
    }
    assert(seen==65535 && s->out_head->len>65000);
    drain(s);
    free(data);
    sl_destroy(s->id);
}

int main(void) {
    for (int v6=0;v6<2;v6++) {
        test_partial_ack_rto(v6);
        test_output_failure(v6);
        test_max_aggregate(v6);
    }
    assert(!contexts && !rx_free && !rx_retained);
    puts("native GSO wire MSS, partial-ACK RTO, output failure and teardown: PASS");
    return 0;
}
