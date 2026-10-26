#ifndef SING_LWIP_OPTIONS_H
#define SING_LWIP_OPTIONS_H
#include <stdint.h>
#if defined(__ANDROID__)
/* Current bionic provides fd_set; disable the legacy port's duplicate typedef. */
#include <sys/select.h>
#ifndef FD_SET_DEFINED
#define FD_SET_DEFINED 1
#endif
#endif
#include "third_party/lwip/src/ports/include/lwipopts.h"
/* Windows uses LLP64: pointers are 64-bit without __LP64__. Reassembly's
 * pointer-bearing helper must not overwrite the eight-byte fragment header. */
#if UINTPTR_MAX > UINT32_MAX
#undef IPV6_FRAG_COPYHEADER
#define IPV6_FRAG_COPYHEADER 1
#endif
#undef IP_REASS_MAX_PBUFS
#define IP_REASS_MAX_PBUFS 64
#undef PBUF_POOL_SIZE
#define PBUF_POOL_SIZE 128
/* Notify the wrapper when a half-closed PCB is actually destroyed. */
#define LWIP_TCP_PCB_NUM_EXT_ARGS 1
#endif
