package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/capture"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestCaptureScopeMaintainsAndFencesAssociationEpoch(t *testing.T) {
	f := newDeviceTest(t)
	id := f.binding.ManagementID
	allowed := map[string]bool{id: true}
	resolve := func() capture.Binding {
		t.Helper()
		witness, err := f.service.ResolveCaptureBinding(t.Context(), id, allowed)
		if err != nil {
			t.Fatal(err)
		}
		return witness
	}
	installCaptureObserver(f)
	f.service.SampleDevices(t.Context())
	witness := resolve()
	if !witness.ValidFor(id) || witness.NativeUUID != f.binding.UUID || witness.Generation != f.decision.Generation ||
		witness.IfIndex != 7 || witness.KernelEpoch == 0 {
		t.Fatalf("unexpected capture witness: %+v", witness)
	}
	f.service.HandleDeviceLinkEvents(DeviceLinkEvents{Indices: []int{99}})
	f.now = f.now.Add(time.Second)
	f.service.SampleDevices(t.Context())
	if current := resolve(); current != witness {
		t.Fatalf("untouched association changed during a regular refresh: %+v to %+v", witness, current)
	}

	for _, event := range []DeviceLinkEvents{{Indices: []int{7}}, {Lost: true}} {
		f.service.HandleDeviceLinkEvents(event)
		_, err := f.service.ResolveCaptureBinding(t.Context(), id, allowed)
		expectCaptureProblem(t, err, 409, "CAPTURE_INTERFACE_UNPROVEN")
		f.service.SampleDevices(t.Context())
		current := resolve()
		if current.KernelEpoch <= witness.KernelEpoch || current.NativeUUID != witness.NativeUUID ||
			current.IfIndex != witness.IfIndex || current.Name != witness.Name {
			t.Fatalf("old epoch revived after invalidation: %+v to %+v", witness, current)
		}
		witness = current
	}
}

func TestCaptureScopeDeniesUnapprovedAmbiguousAndStaleInputs(t *testing.T) {
	f := newDeviceTest(t)
	id := f.binding.ManagementID
	allowed := map[string]bool{id: true}
	installCaptureObserver(f)
	f.service.SampleDevices(t.Context())
	_, err := f.service.ResolveCaptureBinding(t.Context(), id, nil)
	expectCaptureProblem(t, err, 403, "CAPTURE_SCOPE_DENIED")
	_, err = f.service.ResolveCaptureBinding(t.Context(), repository.NewID(), allowed)
	expectCaptureProblem(t, err, 403, "CAPTURE_SCOPE_DENIED")

	other := addEventTestDevice(f, "cap-duplicate", 7)
	f.service.SampleDevices(t.Context())
	_, err = f.service.ResolveCaptureBinding(t.Context(), id, allowed)
	expectCaptureProblem(t, err, 409, "CAPTURE_INTERFACE_UNPROVEN")
	_, err = f.service.ResolveCaptureBinding(t.Context(), other.ManagementID, map[string]bool{other.ManagementID: true})
	expectCaptureProblem(t, err, 409, "CAPTURE_INTERFACE_UNPROVEN")
	f.now = f.now.Add(FreshFor + time.Second)
	_, err = f.service.ResolveCaptureBinding(t.Context(), id, allowed)
	expectCaptureProblem(t, err, 503, "CAPTURE_EVIDENCE_UNAVAILABLE")
}

func installCaptureObserver(f *deviceTest) {
	f.service.SetDeviceObserver(deviceObserverFunc(func(_ context.Context, req DeviceRequest) DeviceSample {
		return DeviceSample{Availability: "known", IfIndex: req.IfIndex, ObservedAt: f.now}
	}))
}

func expectCaptureProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	p, ok := err.(*apitypes.Problem)
	if !ok || p.Status != status || p.Code != code {
		t.Fatalf("capture scope error = %v, want %d %s", err, status, code)
	}
}
