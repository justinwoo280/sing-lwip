/* In-memory ownership/retention regression. Including the bridge lets this
 * test check bounded caching and complete teardown without adding ABI hooks. */
#include "../bridge.c"
#include <lwip/inet_chksum.h>
#include <assert.h>
#include <stdio.h>

static uint64_t create_stack(void) {
    uint8_t config[32]={0};
    put32(config,1500); config[8]=127; config[11]=1; config[31]=1;
    uint64_t id=sl_create(config);
    assert(id);
    return id;
}

static void test_reference_lifetime(void) {
    uint64_t id=create_stack();
    struct pbuf *held=alloc_rx(65535);
    assert(held);
    memset(held->payload,0x51,held->len);
    pbuf_ref(held);
    assert(!pbuf_remove_header(held,60));
    pbuf_realloc(held,40000);
    assert(pbuf_free(held)==0);

    /* A still-referenced, trimmed pbuf must survive other input/reuse. */
    struct pbuf *other=alloc_rx(65535);
    assert(other && other!=held);
    memset(other->payload,0xa3,other->len);
    for (unsigned i=0;i<held->len;i++) assert(((uint8_t *)held->payload)[i]==0x51);
    assert(pbuf_free(other)==1);
    assert(pbuf_free(held)==1);
    assert(rx_retained==2);

    /* Reinitialization must restore the whole buffer after header removal and
     * trimming, with exactly one reference and an independent next pointer. */
    struct pbuf *reused=alloc_rx(65535);
    assert(reused==held && reused->len==65535 && reused->tot_len==65535);
    assert(reused->ref==1 && !reused->next);
    memset(reused->payload,0x37,65535);
    assert(pbuf_free(reused)==1);
    sl_destroy(id);
    assert(!rx_free && !rx_retained);
}

static void queue_udp(uint64_t id, unsigned marker) {
    uint8_t packet[60000]={0};
    packet[0]=0x45; packet[2]=sizeof(packet)>>8; packet[3]=sizeof(packet)&255;
    packet[8]=64; packet[9]=17;
    packet[12]=198; packet[13]=18; packet[15]=10;
    packet[16]=198; packet[17]=18; packet[19]=20;
    packet[22]=0x04; packet[23]=0xd2;
    packet[24]=(sizeof(packet)-20)>>8; packet[25]=(sizeof(packet)-20)&255;
    uint16_t sum=inet_chksum(packet,20);
    memcpy(packet+10,&sum,sizeof(sum));
    for (unsigned i=0;i<48;i++) {
        packet[20]=0x9c; packet[21]=(uint8_t)i;
        memset(packet+28,(int)(i^marker),sizeof(packet)-28);
        assert(sl_input(id,packet,sizeof(packet))==0);
    }
    assert(context(id)->count==48);
}

static void test_burst_and_teardown(void) {
    uint64_t first=create_stack(), second=create_stack();
    struct pbuf *packets[96];
    for (unsigned i=0;i<96;i++) {
        packets[i]=alloc_rx((u16_t)(40000+i));
        assert(packets[i]);
        memset(packets[i]->payload,(int)i,packets[i]->len);
    }
    for (unsigned i=0;i<96;i++) assert(pbuf_free(packets[i])==1);
    assert(rx_retained==SL_RX_RETAIN);

    /* Two netifs retain distinct data for identical endpoints. Destroy one
     * with unread packets, then validate every byte on the other netif. */
    queue_udp(first,0x00);
    queue_udp(second,0x80);
    sl_destroy(first);
    assert(context(second)->count==48);
    uint8_t received[60000];
    unsigned reads=0;
    for (unsigned i=0;i<1024;i++) {
        for (sl_connection *c=connections[i];c;c=c->hash_next) {
            assert(c->owner==context(second));
            int32_t n=sl_read(c->id,received,sizeof(received),NULL);
            assert(n==59972);
            for (int32_t j=0;j<n;j++) assert(received[j]==(uint8_t)(c->address[32]^0x80));
            reads++;
        }
    }
    assert(reads==48);
    /* Reuse the recycled storage, then tear down the final stack with unread
     * packets. All buffers, including the retained cache, must be released. */
    queue_udp(second,0x40);
    sl_destroy(second);
    assert(!contexts && !rx_free && !rx_retained);
    for (unsigned i=0;i<1024;i++) assert(!connections[i]);
}

int main(void) {
    test_reference_lifetime();
    for (unsigned i=0;i<4;i++) test_burst_and_teardown();
    puts("native receive buffer ownership, bounds and teardown: PASS");
    return 0;
}
