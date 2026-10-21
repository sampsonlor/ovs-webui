/* SPDX-License-Identifier: Apache-2.0
 * Compiled only by an explicit opt-in. Runs on ovs-vswitchd's main thread.
 * The handler neither services IDL nor changes OVS configuration/protocols.
 */
#include <openssl/evp.h>
#include "openvswitch/json.h"
#include "ovsdb-data.h"
#include "ovsdb-idl-provider.h"

static uint64_t webui_configuration_epoch;
static uint64_t webui_binding_epoch;
static const char *webui_keys[] = {
    "stp-priority", "stp-hello-time", "stp-max-age", "stp-forward-delay",
    "rstp-priority", "rstp-max-age", "rstp-forward-delay"
};

static void
webui_number(struct json *j, const char *key, uint64_t n)
{
    json_object_put_format(j, key, "%"PRIu64, n);
}

static void
webui_uuid(struct json *j, const char *key, const struct uuid *id)
{
    json_object_put_format(j, key, UUID_FMT, UUID_ARGS(id));
}

static int
webui_string_cmp(const void *a, const void *b)
{
    return strcmp(*(char *const *) a, *(char *const *) b);
}

/* ASCII canonicalization. Hex-encoded typed atoms remove delimiter, escaping
 * and Unicode normalization ambiguities. Input and allocation budgets apply
 * before serialization, independently of the small JSON response budget. */
static char *
webui_atom(const union ovsdb_atom *a, enum ovsdb_atomic_type type)
{
    struct ds raw = DS_EMPTY_INITIALIZER;
    switch (type) {
    case OVSDB_TYPE_STRING: {
        const char *text = json_string(a->s);
        if (strlen(text) > 4096) {
            return NULL;
        }
        ds_put_char(&raw, 's');
        ds_put_cstr(&raw, text);
        break;
    }
    case OVSDB_TYPE_INTEGER:
        ds_put_format(&raw, "i%"PRId64, a->integer);
        break;
    case OVSDB_TYPE_BOOLEAN:
        ds_put_cstr(&raw, a->boolean ? "b1" : "b0");
        break;
    case OVSDB_TYPE_UUID:
        ds_put_format(&raw, "u"UUID_FMT, UUID_ARGS(&a->uuid));
        break;
    case OVSDB_TYPE_VOID:
    case OVSDB_TYPE_REAL:
    case OVSDB_N_TYPES:
        return NULL;
    }
    struct ds hex = DS_EMPTY_INITIALIZER;
    for (size_t i = 0; i < raw.length; i++) {
        ds_put_format(&hex, "%02x", (unsigned char) raw.string[i]);
    }
    ds_destroy(&raw);
    return ds_steal_cstr(&hex);
}

static bool
webui_column(struct ds *image, const struct ovsdb_idl_row *row,
             const struct ovsdb_idl_column *column)
{
    if (!row->old_datum || !row->table->modes ||
        !(row->table->modes[column - row->table->class_->columns]
          & OVSDB_IDL_MONITOR)) {
        return false;
    }
    const struct ovsdb_datum *d = &row->old_datum[
        column - row->table->class_->columns];
    if (d->n > 128) {
        return false;
    }
    char *entries[128];
    size_t n = 0;
    bool ok = true;
    for (size_t i = 0; i < d->n; i++) {
        char *k = webui_atom(&d->keys[i], column->type.key.type);
        char *v = column->type.value.type == OVSDB_TYPE_VOID
            ? xstrdup("") : webui_atom(&d->values[i], column->type.value.type);
        if (!k || !v) {
            free(k);
            free(v);
            ok = false;
            break;
        }
        entries[n++] = xasprintf("%s:%s", k, v);
        free(k);
        free(v);
    }
    qsort(entries, n, sizeof *entries, webui_string_cmp);
    ds_put_format(image, "%s=", column->name);
    for (size_t i = 0; i < n; i++) {
        if (i) {
            ds_put_char(image, ',');
        }
        ds_put_cstr(image, entries[i]);
        free(entries[i]);
        if (image->length > 256 * 1024) {
            ok = false;
        }
    }
    ds_put_char(image, '\n');
    return ok && image->length <= 256 * 1024;
}

static bool
webui_row(struct ds *image, const struct ovsdb_idl_row *row,
          const char *table, const char *const *names, size_t n_names)
{
    ds_put_format(image, "@%s:"UUID_FMT"\n", table, UUID_ARGS(&row->uuid));
    for (size_t i = 0; i < n_names; i++) {
        const struct ovsdb_idl_column *col = NULL;
        for (size_t c = 0; c < row->table->class_->n_columns; c++) {
            if (!strcmp(names[i], row->table->class_->columns[c].name)) {
                col = &row->table->class_->columns[c];
                break;
            }
        }
        if (!col || !webui_column(image, row, col)) {
            return false;
        }
    }
    return true;
}

static int
webui_row_cmp(const void *a, const void *b)
{
    const struct ovsdb_idl_row *ra = *(const struct ovsdb_idl_row *const *) a;
    const struct ovsdb_idl_row *rb = *(const struct ovsdb_idl_row *const *) b;
    return uuid_compare_3way(&ra->uuid, &rb->uuid);
}

/* This registry is fixed to the pinned 3.3.9 schema. Omitted management
 * external_ids and non-consumed output columns are deliberately not attested.
 * References to unmodelled configuration tables withhold the whole witness. */
#include "vswitchd/ovs-webui-input-columns.inc"

static bool
webui_digest(struct bridge *br, const struct ovsrec_open_vswitch *root,
             char digest[65])
{
    if (!root || ovsrec_open_vswitch_next(root) || root->next_cfg < 0 ||
        root->n_bridges > 128 || root->n_datapaths || root->n_manager_options ||
        root->ssl || !br->cfg || !br->ofproto || br->cfg->n_ports > 128 ||
        br->cfg->n_controller || br->cfg->n_mirrors || br->cfg->netflow ||
        br->cfg->sflow || br->cfg->ipfix || br->cfg->auto_attach ||
        br->cfg->n_flow_tables) {
        return false;
    }
    size_t root_links = 0;
    for (size_t i = 0; i < root->n_bridges; i++) {
        root_links += root->bridges[i] == br->cfg;
    }
    if (root_links != 1) {
        return false;
    }
    const struct ovsdb_idl_row *ports[128], *ifaces[128];
    size_t n_ifaces = 0;
    for (size_t p = 0; p < br->cfg->n_ports; p++) {
        const struct ovsrec_port *port = br->cfg->ports[p];
        if (port->n_interfaces != 1 || port->qos) {
            return false;
        }
        ports[p] = &port->header_;
        ifaces[n_ifaces++] = &port->interfaces[0]->header_;
    }
    qsort(ports, br->cfg->n_ports, sizeof *ports, webui_row_cmp);
    qsort(ifaces, n_ifaces, sizeof *ifaces, webui_row_cmp);
    struct ds image = DS_EMPTY_INITIALIZER;
    ds_put_cstr(&image, "ovs-webui.stp-input.v1\n");
    bool ok = webui_row(&image, &root->header_, "Open_vSwitch",
                        webui_root_columns, ARRAY_SIZE(webui_root_columns)) &&
              webui_row(&image, &br->cfg->header_, "Bridge",
                        webui_bridge_columns, ARRAY_SIZE(webui_bridge_columns));
    for (size_t i = 0; ok && i < br->cfg->n_ports; i++) {
        if (i && uuid_equals(&ports[i - 1]->uuid, &ports[i]->uuid)) {
            ok = false;
            break;
        }
        ok = webui_row(&image, ports[i], "Port", webui_port_columns,
                       ARRAY_SIZE(webui_port_columns));
    }
    for (size_t i = 0; ok && i < n_ifaces; i++) {
        if (i && uuid_equals(&ifaces[i - 1]->uuid, &ifaces[i]->uuid)) {
            ok = false;
            break;
        }
        const struct ovsrec_interface *cfg = (const void *) ifaces[i];
        struct iface *iface = iface_lookup(br, cfg->name);
        if (!iface || iface->cfg != cfg || iface->ofp_port == OFPP_NONE ||
            (strcmp(cfg->type, "") && strcmp(cfg->type, "system") &&
             strcmp(cfg->type, "internal"))) {
            ok = false;
            break;
        }
        ok = webui_row(&image, ifaces[i], "Interface", webui_iface_columns,
                       ARRAY_SIZE(webui_iface_columns));
        ds_put_format(&image, "ofport=%u\n", ofp_to_u16(iface->ofp_port));
    }
    if (ok && image.length <= 256 * 1024) {
        unsigned char hash[EVP_MAX_MD_SIZE];
        unsigned int n = 0;
        ok = EVP_Digest(image.string, image.length, hash, &n,
                        EVP_sha256(), NULL) == 1 && n == 32;
        if (ok) {
            for (size_t i = 0; i < 32; i++) {
                snprintf(digest + 2 * i, 3, "%02x", hash[i]);
            }
        }
    }
    ds_destroy(&image);
    return ok;
}

static void
webui_begin(struct bridge *br)
{
    br->webui.valid = false;
    br->webui.setter_error = false;
    br->webui.failure = NULL;
    webui_epoch_advance(&webui_configuration_epoch);
    br->webui.configuration_epoch = webui_configuration_epoch;
}

static void
webui_finish(struct bridge *br, const struct ovsrec_open_vswitch *root)
{
    struct webui_bridge *w = &br->webui;
    struct webui_native native;
    if (w->setter_error) {
        return;
    }
    if (!webui_digest(br, root, w->digest)) {
        w->failure = "BINDING_UNPROVEN";
        return;
    }
    if (ofproto_webui_snapshot(br->ofproto, &native)) {
        w->failure = "PROVIDER_SNAPSHOT_UNSUPPORTED";
        return;
    }
    if (!uuid_equals(&w->configured_uuid, &br->cfg->header_.uuid) ||
        !uuid_equals(&w->runtime_instance, &native.instance)) {
        webui_epoch_advance(&webui_binding_epoch);
        w->binding_epoch = webui_binding_epoch;
    }
    w->configured_uuid = br->cfg->header_.uuid;
    w->runtime_instance = native.instance;
    w->root = root->header_.uuid;
    w->next_cfg = root->next_cfg;
    w->seqno = ovsdb_idl_get_seqno(idl);
    w->authority_epoch = ovsdb_idl_webui_epoch(idl);
    json_destroy(w->source);
    w->source = json_object_create();
    json_object_put(w->source, "stp_enable", json_boolean_create(br->cfg->stp_enable));
    json_object_put(w->source, "rstp_enable", json_boolean_create(br->cfg->rstp_enable));
    struct json *parameters = json_object_create();
    json_object_put(w->source, "parameters", parameters);
    for (size_t i = 0; i < ARRAY_SIZE(webui_keys); i++) {
        const char *v = smap_get(&br->cfg->other_config, webui_keys[i]);
        if (v && strlen(v) > 128) {
            w->failure = "WITNESS_BUDGET";
            return;
        }
        json_object_put(parameters, webui_keys[i],
                        v ? json_string_create(v) : json_null_create());
    }
    w->valid = w->binding_epoch != UINT64_MAX &&
        w->configuration_epoch != UINT64_MAX && w->authority_epoch != UINT64_MAX;
}

static struct json *
webui_engine(bool enabled, const struct webui_local *s)
{
    struct json *j = json_object_create();
    json_object_put(j, "enabled", json_boolean_create(enabled));
    struct json *local = enabled ? json_object_create() : json_null_create();
    json_object_put(j, "local", local);
    if (enabled) {
        struct eth_addr mac;
        eth_addr_from_uint64(s->bridge_id & ((UINT64_C(1) << 48) - 1), &mac);
        json_object_put_format(local, "system_id", ETH_ADDR_FMT, ETH_ADDR_ARGS(mac));
        json_object_put(local, "priority", json_integer_create(s->bridge_id >> 48));
        json_object_put(local, "hello_time_ms", json_integer_create(s->hello_ms));
        json_object_put(local, "max_age_ms", json_integer_create(s->max_age_ms));
        json_object_put(local, "forward_delay_ms", json_integer_create(s->forward_delay_ms));
    }
    return j;
}

static void
webui_show(struct unixctl_conn *conn, int argc OVS_UNUSED,
           const char *argv[], void *aux OVS_UNUSED)
{
    struct uuid requested;
    char canonical[UUID_LEN + 1];
    if (!uuid_from_string(&requested, argv[1])) {
        unixctl_command_reply_error(conn, "INVALID_WITNESS_ARGUMENT");
        return;
    }
    snprintf(canonical, sizeof canonical, UUID_FMT, UUID_ARGS(&requested));
    if (strcmp(canonical, argv[1]) || strlen(argv[2]) != 64 ||
        strspn(argv[2], "0123456789abcdef") != 64) {
        unixctl_command_reply_error(conn, "INVALID_WITNESS_ARGUMENT");
        return;
    }
    struct json *reply = json_object_create();
    json_object_put_string(reply, "schema_version", "ovs-webui.stp-witness.v1");
    json_object_put_string(reply, "nonce", argv[2]);
    const char *reason = NULL;
    struct bridge *br = NULL, *b;
    const struct ovsrec_open_vswitch *root = ovsrec_open_vswitch_first(idl);
    uint64_t authority = ovsdb_idl_webui_epoch(idl);
    if (authority == UINT64_MAX || webui_configuration_epoch == UINT64_MAX ||
        webui_binding_epoch == UINT64_MAX) {
        reason = "EPOCH_EXHAUSTED";
    } else if (!ovsdb_idl_is_connected(idl)) {
        reason = "IDL_NOT_CONNECTED";
    } else if (ovsdb_idl_is_lock_contended(idl)) {
        reason = "IDL_LOCK_CONTENDED";
    } else if (!ovsdb_idl_has_lock(idl)) {
        reason = "IDL_LOCK_UNOWNED";
    }
    HMAP_FOR_EACH (b, node, &all_bridges) {
        if (uuid_equals(&b->webui.configured_uuid, &requested)) {
            if (br) {
                reason = "BINDING_UNPROVEN";
            }
            br = b;
        }
    }
    struct webui_native native;
    char digest[65];
    if (!reason && !br) {
        reason = "BRIDGE_NOT_CONFIGURED";
    }
    if (!reason && (br->webui.configuration_epoch == UINT64_MAX ||
                    br->webui.binding_epoch == UINT64_MAX)) {
        reason = "EPOCH_EXHAUSTED";
    }
    if (!reason && br->webui.setter_error) {
        reason = "SETTER_REPORTED_ERROR";
    }
    if (!reason && br->webui.failure) {
        reason = br->webui.failure;
    }
    if (!reason && (!br->webui.valid || !root ||
        !uuid_equals(&br->webui.configured_uuid, &br->cfg->header_.uuid) ||
        !uuid_equals(&br->webui.root, &root->header_.uuid) ||
        br->webui.authority_epoch != authority ||
        br->webui.seqno != ovsdb_idl_get_seqno(idl) ||
        br->webui.next_cfg != root->next_cfg || root->cur_cfg != root->next_cfg)) {
        reason = "CONFIGURATION_IN_PROGRESS";
    }
    if (!reason && (!webui_digest(br, root, digest) ||
                     strcmp(digest, br->webui.digest))) {
        reason = "BINDING_UNPROVEN";
    }
    if (!reason && ofproto_webui_snapshot(br->ofproto, &native)) {
        reason = "PROVIDER_SNAPSHOT_UNSUPPORTED";
    }
    if (!reason && (!uuid_equals(&native.instance, &br->webui.runtime_instance) ||
                    authority != ovsdb_idl_webui_epoch(idl))) {
        reason = "BINDING_UNPROVEN";
    }
    if (reason) {
        json_object_put_string(reply, "state", "withheld");
        json_object_put_string(reply, "reason", reason);
    } else {
        json_object_put_string(reply, "state", "known");
        webui_uuid(reply, "daemon_instance_id", &webui_daemon_instance);
        struct json *db = json_object_create(), *bridge = json_object_create();
        struct json *cfg = json_object_create(), *engines = json_object_create();
        json_object_put(reply, "database", db);
        json_object_put(reply, "bridge", bridge);
        json_object_put(reply, "configuration", cfg);
        json_object_put(reply, "native", engines);
        webui_uuid(db, "root_uuid", &br->webui.root);
        json_object_put(db, "connected", json_boolean_create(true));
        json_object_put_string(db, "lock_name", "ovs_vswitchd");
        json_object_put(db, "lock_owned", json_boolean_create(true));
        json_object_put(db, "lock_contended", json_boolean_create(false));
        webui_number(db, "authority_epoch", authority);
        webui_number(db, "idl_seqno", br->webui.seqno);
        webui_uuid(bridge, "ovs_uuid", &br->webui.configured_uuid);
        webui_uuid(bridge, "runtime_instance_id", &native.instance);
        json_object_put_string(bridge, "name", br->name);
        webui_number(bridge, "binding_epoch", br->webui.binding_epoch);
        webui_number(cfg, "epoch", br->webui.configuration_epoch);
        webui_number(cfg, "idl_seqno", br->webui.seqno);
        webui_number(cfg, "root_next_cfg", br->webui.next_cfg);
        json_object_put_string(cfg, "completion", "complete");
        json_object_put(cfg, "setters_reported_error", json_boolean_create(false));
        json_object_put_string(cfg, "runtime_input_format", "ovs-webui.stp-input.v1");
        json_object_put_string(cfg, "runtime_input_digest", br->webui.digest);
        json_object_put(cfg, "source", json_clone(br->webui.source));
        json_object_put(engines, "stp", webui_engine(native.stp_enabled, &native.stp));
        json_object_put(engines, "rstp", webui_engine(native.rstp_enabled, &native.rstp));
    }
    char *text = json_to_string(reply, JSSF_SORT);
    if (strlen(text) > 16 * 1024) {
        free(text);
        json_destroy(reply);
        reply = json_object_create();
        json_object_put_string(reply, "schema_version", "ovs-webui.stp-witness.v1");
        json_object_put_string(reply, "nonce", argv[2]);
        json_object_put_string(reply, "state", "withheld");
        json_object_put_string(reply, "reason", "WITNESS_BUDGET");
        text = json_to_string(reply, JSSF_SORT);
    }
    unixctl_command_reply(conn, text);
    free(text);
    json_destroy(reply);
}
