package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/openflowview"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
)

type openFlowViewer struct {
	source *ovsdb.OpenFlowObserver
	views  *openflowview.Snapshots
}

func (r *Repository) WithOpenFlow(observer *ovsdb.OpenFlowObserver) *Repository {
	if observer != nil {
		r.openFlow = &openFlowViewer{source: observer, views: openflowview.New()}
	}
	return r
}

func openFlowScope(claims authn.Claims) openflowview.Scope {
	return openflowview.Scope{Principal: claims.PrincipalID, Credential: claims.CredentialID, PermissionRevision: claims.Revision}
}

func parseOpenFlowQuery(id string, query url.Values) (ovsdb.OpenFlowQuery, error) {
	out := ovsdb.OpenFlowQuery{BridgeID: id, Limit: ovsdb.MaxOpenFlowRows,
		IncludeGroupsMeters: query.Get("groups_meters") == "true", RawFields: query.Get("raw_fields") == "true"}
	table, err := strconv.ParseUint(query.Get("table"), 10, 8)
	if err != nil || table >= 255 {
		return out, apitypes.Fail(422, "INVALID_OPENFLOW_TABLE")
	}
	number := uint8(table)
	out.Table = &number
	if cookie := query.Get("cookie"); cookie != "" {
		value, err := strconv.ParseUint(cookie, 10, 64)
		if err != nil || strconv.FormatUint(value, 10) != cookie {
			return out, apitypes.Fail(422, "INVALID_OPENFLOW_COOKIE")
		}
		out.Cookie = &value
	}
	if limit := query.Get("row_limit"); limit != "" {
		out.Limit, err = strconv.Atoi(limit)
		if err != nil || out.Limit < 1 || out.Limit > ovsdb.MaxOpenFlowRows {
			return out, apitypes.Fail(422, "INVALID_OPENFLOW_LIMIT")
		}
	}
	return out, nil
}

type openFlowPage struct {
	Snapshot openflowview.Reference `json:"snapshot"`
	Evidence ovsdb.OpenFlowSnapshot `json:"evidence"`
	Offset   int                    `json:"offset"`
	Next     *int                   `json:"next_offset,omitempty"`
}

// OpenFlow queries never hold the repository transaction while waiting for the
// qualified local provider. Both the original and current grant are checked.
func (r *Repository) readScopedOpenFlow(ctx context.Context, credential string, op *apicontract.Operation, path map[string]string, query url.Values) (authn.Response, error) {
	var claims authn.Claims
	rawFields := query.Get("raw_fields") == "true"
	check := func(ctx context.Context, q querier, id string) (authn.Claims, error) {
		var refs []apitypes.Ref
		if id != "" {
			refs = []apitypes.Ref{{Kind: "bridge", ID: id}}
		}
		c, err := r.checkOperation(ctx, q, credential, op, nil, false)
		if err == nil {
			err = require(c, "inventory.read", false, 0)
		}
		if err == nil && rawFields {
			err = require(c, "configuration.read", false, 0)
		}
		if err == nil {
			err = checkRefs(ctx, q, c, refs)
		}
		return c, err
	}
	bridgeID := path["bridge_id"]
	if err := r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var err error
		claims, err = check(ctx, q, bridgeID)
		return err
	}); err != nil {
		return authn.Response{}, err
	}
	if r.openFlow == nil {
		return authn.Response{}, apitypes.Fail(503, "OPENFLOW_SOURCE_UNAVAILABLE")
	}
	view := openFlowPage{}
	var captured ovsdb.OpenFlowSnapshot
	scope := openFlowScope(claims)
	if bridgeID != "" {
		input, err := parseOpenFlowQuery(bridgeID, query)
		if err != nil {
			return authn.Response{}, err
		}
		captured, err = r.openFlow.source.ObserveFlows(ctx, input)
		if err != nil {
			return authn.Response{}, err
		}
		if captured.Query.RawFields != input.RawFields {
			return authn.Response{}, apitypes.Fail(503, "OPENFLOW_RESPONSE_INVALID")
		}
	} else {
		var err error
		view.Snapshot, captured, err = r.openFlow.views.Read(r.now(), scope, path["snapshot_id"])
		if err != nil {
			return authn.Response{}, err
		}
		bridgeID = view.Snapshot.BridgeID
	}
	rawFields = captured.Query.RawFields
	var response authn.Response
	err := r.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		current, err := check(ctx, tx, bridgeID)
		if err != nil {
			return err
		}
		if openFlowScope(current) != scope {
			return apitypes.Fail(403, "OPENFLOW_AUTHORITY_CHANGED")
		}
		source, ok := r.inventory.(interface {
			OpenFlowSource(context.Context, string) (inventory.OpenFlowBridgeSource, error)
		})
		if !ok {
			return apitypes.Fail(503, "OPENFLOW_INVENTORY_UNAVAILABLE")
		}
		witness, err := source.OpenFlowSource(ctx, bridgeID)
		if err != nil || witness.Generation != captured.InstanceGeneration {
			return apitypes.Fail(409, "OPENFLOW_SCOPE_CHANGED")
		}
		if view.Snapshot.ID == "" {
			view.Snapshot, err = r.openFlow.views.Put(r.now(), scope, captured)
			if err != nil {
				return err
			}
			_, captured, err = r.openFlow.views.Read(r.now(), scope, view.Snapshot.ID)
			if err != nil {
				return err
			}
		} else if !r.now().Before(view.Snapshot.ExpiresAt) {
			return apitypes.Fail(404, "OPENFLOW_SNAPSHOT_UNAVAILABLE")
		}
		view.Evidence = captured
		if query.Get("export") != "true" {
			offset, _ := strconv.Atoi(query.Get("offset"))
			pageRows, _ := strconv.Atoi(query.Get("page_size"))
			if pageRows == 0 {
				pageRows = openflowview.MaxPageRows
			}
			if offset > len(captured.Rows) {
				return apitypes.Fail(422, "INVALID_OPENFLOW_PAGE")
			}
			end := min(offset+pageRows, len(captured.Rows))
			view.Evidence.Rows, view.Offset = captured.Rows[offset:end], offset
			if end < len(captured.Rows) {
				view.Next = &end
			}
		}
		response.Status = 200
		response.Body, err = json.Marshal(view)
		if err != nil {
			return err
		}
		if len(response.Body) > 48<<10 {
			return apitypes.Fail(429, "RESPONSE_BUDGET_EXCEEDED")
		}
		if err := op.ValidateResponse(response.Status, response.Body); err != nil {
			return err
		}
		return r.touch(ctx, tx, credential, current)
	})
	if err != nil {
		return authn.Response{}, err
	}
	return response, nil
}
