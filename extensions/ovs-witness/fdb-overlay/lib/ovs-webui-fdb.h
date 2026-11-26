/* SPDX-License-Identifier: Apache-2.0
 * Optional bounded, read-only OVS MAC learning observation. */
#ifndef OVS_WEBUI_FDB_H
#define OVS_WEBUI_FDB_H 1

#ifdef ENABLE_OVS_WEBUI_WITNESS
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include "openvswitch/types.h"
#include "uuid.h"

#define WEBUI_FDB_MAX_ROWS 256
#define WEBUI_FDB_PORT_NAME_MAX 64

struct ofproto;

struct webui_fdb_row {
    struct eth_addr mac;
    uint16_t vlan;
    uint16_t ofport;
    int age;
    bool is_static;
    char port_name[WEBUI_FDB_PORT_NAME_MAX + 1];
};

struct webui_fdb_sample {
    size_t total;
    size_t n;
    struct webui_fdb_row rows[WEBUI_FDB_MAX_ROWS];
};

int ofproto_webui_fdb_sample(struct ofproto *, const struct uuid *,
                            struct webui_fdb_sample *);
#endif

#endif
