/* SPDX-License-Identifier: Apache-2.0 */

static bool
webui_fdb_bridge_name(const char *name)
{
    size_t len = strnlen(name, 16);
    if (!len || len > 15) {
        return false;
    }
    for (size_t i = 0; i < len; i++) {
        char c = name[i];
        if (!((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')
              || (c >= 'a' && c <= 'z')
              || (i && (c == '_' || c == '-' || c == '.')))) {
            return false;
        }
    }
    return true;
}

static bool
webui_fdb_bridge_installed(const struct bridge *br, const struct uuid *uuid)
{
    const struct ovsrec_open_vswitch *root = ovsrec_open_vswitch_first(idl);
    uint64_t authority = ovsdb_idl_webui_epoch(idl);

    return br && br->cfg && br->ofproto && root
        && authority != UINT64_MAX
        && webui_configuration_epoch != UINT64_MAX
        && webui_binding_epoch != UINT64_MAX
        && ovsdb_idl_is_connected(idl)
        && !ovsdb_idl_is_lock_contended(idl) && ovsdb_idl_has_lock(idl)
        && br->webui.valid && !br->webui.setter_error && !br->webui.failure
        && br->webui.configuration_epoch != UINT64_MAX
        && br->webui.binding_epoch != UINT64_MAX
        && uuid_equals(&br->webui.configured_uuid, uuid)
        && uuid_equals(&br->cfg->header_.uuid, uuid)
        && uuid_equals(&br->webui.root, &root->header_.uuid)
        && br->webui.authority_epoch == authority
        && br->webui.seqno == ovsdb_idl_get_seqno(idl)
        && br->webui.next_cfg == root->next_cfg && root->cur_cfg == root->next_cfg;
}

static void
webui_fdb_sample(struct unixctl_conn *conn, int argc OVS_UNUSED,
                 const char *argv[], void *aux OVS_UNUSED)
{
    struct uuid requested;
    char canonical[UUID_LEN + 1];
    if (!uuid_from_string(&requested, argv[1]) || !webui_fdb_bridge_name(argv[2])) {
        unixctl_command_reply_error(conn, "INVALID_FDB_ARGUMENT");
        return;
    }
    snprintf(canonical, sizeof canonical, UUID_FMT, UUID_ARGS(&requested));
    if (strcmp(canonical, argv[1])) {
        unixctl_command_reply_error(conn, "INVALID_FDB_ARGUMENT");
        return;
    }

    const struct bridge *br = bridge_lookup(argv[2]);
    if (!webui_fdb_bridge_installed(br, &requested)) {
        unixctl_command_reply_error(conn, "FDB_SOURCE_UNAVAILABLE");
        return;
    }

    struct webui_fdb_sample *sample = xzalloc(sizeof *sample);
    if (ofproto_webui_fdb_sample(br->ofproto, &br->webui.runtime_instance, sample)) {
        free(sample);
        unixctl_command_reply_error(conn, "FDB_SOURCE_UNAVAILABLE");
        return;
    }
    for (size_t i = 0; i < sample->n; i++) {
        if (sample->rows[i].vlan > 4095 || sample->rows[i].age < 0) {
            free(sample);
            unixctl_command_reply_error(conn, "FDB_SOURCE_UNAVAILABLE");
            return;
        }
    }

    struct json *reply = json_object_create();
    struct json *rows = json_array_create_empty();
    json_object_put(reply, "version", json_integer_create(1));
    json_object_put_string(reply, "bridge_uuid", canonical);
    json_object_put_string(reply, "bridge_name", br->name);
    json_object_put(reply, "total", json_integer_create(sample->total));
    json_object_put(reply, "rows", rows);
    for (size_t i = 0; i < sample->n; i++) {
        const struct webui_fdb_row *row = &sample->rows[i];
        struct json *item = json_object_create();
        json_object_put_format(item, "mac", ETH_ADDR_FMT, ETH_ADDR_ARGS(row->mac));
        json_object_put(item, "vlan", json_integer_create(row->vlan));
        json_object_put(item, "ofport", row->ofport
                        ? json_integer_create(row->ofport) : json_null_create());
        json_object_put(item, "port_name", row->port_name[0]
                        ? json_string_create(row->port_name) : json_null_create());
        json_object_put(item, "age_seconds", row->is_static
                        ? json_null_create() : json_integer_create(row->age));
        json_array_add(rows, item);
    }
    free(sample);

    char *text = json_to_string(reply, JSSF_SORT);
    if (strlen(text) > 64 * 1024) {
        unixctl_command_reply_error(conn, "FDB_SOURCE_UNAVAILABLE");
    } else {
        unixctl_command_reply(conn, text);
    }
    free(text);
    json_destroy(reply);
}
