/* SPDX-License-Identifier: Apache-2.0
 * Optional experimental observation only; no execution authority. */
#ifndef OVS_WEBUI_WITNESS_H
#define OVS_WEBUI_WITNESS_H 1
#include <stdbool.h>
#include <stdint.h>
#include "openvswitch/uuid.h"

/* Saturates, never wraps. UINT64_MAX is terminal, not a valid witness epoch. */
static inline void
webui_epoch_advance(uint64_t *epoch)
{
    if (*epoch != UINT64_MAX) {
        ++*epoch;
    }
}

#ifdef ENABLE_OVS_WEBUI_WITNESS
struct stp;
struct rstp;
struct ofproto;
struct webui_local {
    uint64_t bridge_id;
    uint32_t hello_ms, max_age_ms, forward_delay_ms;
};
struct webui_native {
    struct uuid instance;
    bool stp_enabled, rstp_enabled;
    struct webui_local stp, rstp;
};
void stp_webui_snapshot(const struct stp *, struct webui_local *);
void rstp_webui_snapshot(const struct rstp *, struct webui_local *);
int ofproto_webui_snapshot(struct ofproto *, struct webui_native *);

struct webui_bridge {
    bool valid, setter_error;
    struct uuid configured_uuid, runtime_instance, root;
    uint64_t binding_epoch, configuration_epoch, authority_epoch;
    unsigned int seqno;
    int64_t next_cfg;
    char digest[65];
    struct json *source;
};
#endif
#endif
