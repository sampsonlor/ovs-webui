package web

import (
	"context"
	"errors"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
)

type vlanImportDispatchManager struct {
	authn.Manager
	checked string
}

func (m *vlanImportDispatchManager) CheckAuth(_ context.Context, _ string, check authn.Check) (authn.Claims, error) {
	m.checked = check.Capability
	return authn.Claims{}, apitypes.Fail(403, "CAPABILITY_DENIED")
}

func (*vlanImportDispatchManager) ExecuteAuth(context.Context, string, authn.Command) (apitypes.Result, error) {
	return apitypes.Result{}, apitypes.Fail(503, "DOMAIN_SERVICE_UNAVAILABLE")
}

func TestVLANImportUsesWorkspaceDispatcher(t *testing.T) {
	manager := &vlanImportDispatchManager{}
	auth := NewAuthentication(manager, nil).WithWorkspace(&Workspace{auth: manager})
	op := &apicontract.Operation{ID: "importVLANConfiguration", Domain: "workspace"}
	_, err := auth.Execute(t.Context(), publicapi.Subject{}, publicapi.Query{Operation: op}, requests.Command{Domain: op.Domain})
	var problem *apitypes.Problem
	if !errors.As(err, &problem) || problem.Code != "CAPABILITY_DENIED" || manager.checked != "configuration.import" {
		t.Fatalf("VLAN import did not reach workspace authorization: %v, checked %q", err, manager.checked)
	}
}
