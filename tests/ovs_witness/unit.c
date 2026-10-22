/* SPDX-License-Identifier: Apache-2.0 */
#include <config.h>
#include <errno.h>
#include <pthread.h>
#include <stdio.h>
#include "ovs-webui-witness.h"
#include "ofproto/ofproto-provider.h"
#include "stp.h"
#include "rstp.h"
#include "util.h"

static void *
change_priority(void *arg)
{
    struct stp *s = arg;
    for (int i = 0; i < 10000; i++) {
        stp_set_bridge_priority(s, (i % 2) ? 4096 : 32768);
    }
    return NULL;
}

int
main(int argc OVS_UNUSED, char *argv[])
{
    set_program_name(argv[0]);
    uint64_t epoch = 0;
    webui_epoch_advance(&epoch);
    ovs_assert(epoch == 1);
    epoch = UINT64_MAX - 2;
    webui_epoch_advance(&epoch);
    ovs_assert(epoch == UINT64_MAX - 1);
    webui_epoch_advance(&epoch);
    webui_epoch_advance(&epoch);
    ovs_assert(epoch == UINT64_MAX);
    puts("PASS epoch_saturates_without_ABA");

    const struct ofproto_class unsupported = {0};
    struct ofproto fake = {0};
    struct webui_native native;
    fake.ofproto_class = &unsupported;
    ovs_assert(ofproto_webui_snapshot(&fake, &native) == EOPNOTSUPP);
    puts("PASS unsupported_provider_is_not_disabled");

    stp_init();
    struct stp *stp = stp_create("witness-unit", UINT64_C(0x8000020000000001), NULL, NULL);
    struct webui_local local;
    stp_webui_snapshot(stp, &local);
    ovs_assert(local.bridge_id == UINT64_C(0x8000020000000001));
    ovs_assert(local.hello_ms == 2000 && local.max_age_ms == 20000 && local.forward_delay_ms == 15000);
    stp_set_hello_time(stp, 2);
    stp_webui_snapshot(stp, &local);
    ovs_assert(local.hello_ms == 1000);
    puts("PASS stp_snapshot_keeps_native_millisecond_clamping");

    pthread_t writer;
    ovs_assert(!pthread_create(&writer, NULL, change_priority, stp));
    for (int i = 0; i < 10000; i++) {
        stp_webui_snapshot(stp, &local);
        ovs_assert(local.bridge_id == UINT64_C(0x1000020000000001) ||
                   local.bridge_id == UINT64_C(0x8000020000000001));
        ovs_assert(local.hello_ms == 1000 && local.max_age_ms == 20000 && local.forward_delay_ms == 15000);
    }
    ovs_assert(!pthread_join(writer, NULL));
    stp_unref(stp);
    puts("PASS local_snapshot_mutex_and_lifetime_stress");

    rstp_init();
    struct rstp *rstp = rstp_create("witness-rstp-unit", UINT64_C(0x8000020000000001), NULL, NULL);
    rstp_webui_snapshot(rstp, &local);
    ovs_assert(local.hello_ms == 2000 && local.max_age_ms == 20000 && local.forward_delay_ms == 15000);
    rstp_set_bridge_max_age(rstp, 14);
    rstp_set_bridge_forward_delay(rstp, 8);
    rstp_set_bridge_max_age(rstp, 20);
    rstp_webui_snapshot(rstp, &local);
    ovs_assert(local.max_age_ms == 14000 && local.forward_delay_ms == 8000);
    rstp_unref(rstp);
    puts("PASS rstp_snapshot_reports_retained_local_value");
    return 0;
}
