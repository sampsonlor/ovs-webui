package inventory

import "time"

// HostAccelerationObservation is independent kernel evidence about global
// resources. It carries no claim about a process, device, affinity or OVS.
type HostAccelerationObservation struct {
	State      string                    `json:"state"`
	Reason     string                    `json:"reason_code,omitempty"`
	Source     string                    `json:"source"`
	ObservedAt *time.Time                `json:"observed_at,omitempty"`
	HugePages  []HugePagePool            `json:"huge_pages"`
	OnlineNUMA AccelerationField[uint16] `json:"online_numa_nodes"`
}

type HugePagePool struct {
	SizeKiB         uint32                    `json:"size_kib"`
	PersistentPages AccelerationField[uint64] `json:"persistent_pages"`
	FreePages       AccelerationField[uint64] `json:"free_pages"`
	ReservedPages   AccelerationField[uint64] `json:"reserved_pages"`
	SurplusPages    AccelerationField[uint64] `json:"surplus_pages"`
}
