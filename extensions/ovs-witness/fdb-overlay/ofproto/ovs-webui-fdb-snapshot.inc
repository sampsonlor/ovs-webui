/* SPDX-License-Identifier: Apache-2.0 */

static void
webui_fdb_copy_port(const struct ofbundle *bundle, struct webui_fdb_row *row)
{
    if (!bundle || ovs_list_is_empty(&bundle->ports)) {
        return;
    }

    unsigned int ofport = ofp_to_u16(ofbundle_get_a_port(bundle)->up.ofp_port);
    if (ofport && ofport <= 65534) {
        row->ofport = ofport;
    }

    /* Bundle names describe logical OVS Ports, including bonds.  The
     * accompanying OpenFlow number identifies a representative member. */
    if (!bundle->name) {
        return;
    }
    size_t len = strnlen(bundle->name, sizeof row->port_name);
    if (len == sizeof row->port_name) {
        return;
    }
    for (size_t i = 0; i < len; i++) {
        unsigned char c = bundle->name[i];
        if (c < 0x20 || c > 0x7e) {
            return;
        }
    }
    memcpy(row->port_name, bundle->name, len);
}

/* Main-thread identity validation followed by a single bounded native copy.
 * A busy MAC-learning writer must not delay the switch control loop. */
int
ofproto_webui_fdb_sample(struct ofproto *ofproto_, const struct uuid *instance,
                        struct webui_fdb_sample *sample)
{
    if (ofproto_->ofproto_class != &ofproto_dpif_class
        || !uuid_equals(&ofproto_->webui_instance, instance)) {
        return EOPNOTSUPP;
    }
    struct ofproto_dpif *ofproto = ofproto_dpif_cast(ofproto_);
    struct mac_learning *ml = ofproto->ml;
    if (ovs_rwlock_tryrdlock(&ml->rwlock)) {
        return EBUSY;
    }

    sample->total = hmap_count(&ml->table);
    const struct mac_entry *entry;
    LIST_FOR_EACH_REVERSE (entry, lru_node, &ml->lrus) {
        if (sample->n == WEBUI_FDB_MAX_ROWS) {
            break;
        }
        struct webui_fdb_row *row = &sample->rows[sample->n++];
        row->mac = entry->mac;
        row->vlan = entry->vlan;
        int age = mac_entry_age(ml, entry);
        row->is_static = age == MAC_ENTRY_AGE_STATIC_ENTRY;
        row->age = row->is_static ? 0 : age;
        webui_fdb_copy_port(mac_entry_get_port(ml, entry), row);
    }
    ovs_rwlock_unlock(&ml->rwlock);

    size_t expected = MIN(sample->total, WEBUI_FDB_MAX_ROWS);
    return sample->n == expected ? 0 : EINVAL;
}
