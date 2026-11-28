package ovsdb

import (
	"encoding/json"
	"errors"
	"io"
)

const (
	ofMultipartGroupDesc   = 7
	ofMultipartMeterConfig = 10
	openFlowAuxiliaryRows  = 32
	openFlowAuxiliaryBytes = 8 << 10
)

type OpenFlowGroupBucket struct {
	Weight      uint16   `json:"weight"`
	ActionTypes []uint16 `json:"action_types"`
}

type OpenFlowGroup struct {
	ID                uint32                `json:"group_id"`
	TypeCode          uint8                 `json:"type_code"`
	Buckets           []OpenFlowGroupBucket `json:"buckets"`
	MetadataTruncated bool                  `json:"metadata_truncated"`
}

type OpenFlowMeterBand struct {
	TypeCode uint16 `json:"type_code"`
	Rate     uint32 `json:"rate"`
	Burst    uint32 `json:"burst"`
}

type OpenFlowMeter struct {
	ID                uint32              `json:"meter_id"`
	Flags             uint16              `json:"flags"`
	Bands             []OpenFlowMeterBand `json:"bands"`
	MetadataTruncated bool                `json:"metadata_truncated"`
}

type openFlowAuxiliary struct {
	groupState, groupReason string
	groups                  []OpenFlowGroup
	meterState, meterReason string
	meters                  []OpenFlowMeter
}

func fixedOpenFlowMultipart(body []byte) bool {
	if len(body) < 8 || !openFlowZero(body[2:8]) {
		return false
	}
	switch ofOrder.Uint16(body[:2]) {
	case ofMultipartFlow:
		return len(body) == 48 && ofOrder.Uint16(body[40:42]) == ofMatchOXM &&
			ofOrder.Uint16(body[42:44]) == 4 && openFlowZero(body[44:])
	case ofMultipartGroupDesc:
		return len(body) == 8
	case ofMultipartMeterConfig:
		return len(body) == 16 && ofOrder.Uint32(body[8:12]) == ^uint32(0) && openFlowZero(body[12:])
	default:
		return false
	}
}

func readOpenFlowAuxiliary(w io.Writer, r *openFlowReader, flowRows int, flowLimited bool, out *openFlowAuxiliary) error {
	out.groupState, out.groupReason = "unavailable", "OPENFLOW_CAPTURE_LIMIT"
	out.meterState, out.meterReason = "unavailable", "OPENFLOW_CAPTURE_LIMIT"
	left := MaxOpenFlowRows - flowRows
	if flowLimited || left <= 0 || r.parts >= openFlowReplyParts || r.budget < 16 {
		return nil
	}
	request := make([]byte, 8)
	ofOrder.PutUint16(request[:2], ofMultipartGroupDesc)
	if err := sendOpenFlow(w, ofMultipartRequest, 4, request); err != nil {
		return err
	}
	groups, limited, err := readOpenFlowDescriptions(r, 4, ofMultipartGroupDesc, min(left, openFlowAuxiliaryRows), decodeOpenFlowGroup)
	if errors.Is(err, errOpenFlowUnsupported) {
		out.groupReason = "OPENFLOW_GROUP_DESCRIPTION_UNSUPPORTED"
	} else if err != nil {
		return err
	} else {
		out.groupState, out.groupReason = openFlowDescriptionState(limited)
		out.groups = groups
	}
	left -= len(groups)
	if limited || left <= 0 || r.parts >= openFlowReplyParts || r.budget < 16 {
		return nil
	}
	request = make([]byte, 16)
	ofOrder.PutUint16(request[:2], ofMultipartMeterConfig)
	ofOrder.PutUint32(request[8:12], ^uint32(0)) // OFPM_ALL
	if err := sendOpenFlow(w, ofMultipartRequest, 5, request); err != nil {
		return err
	}
	meters, limited, err := readOpenFlowDescriptions(r, 5, ofMultipartMeterConfig, min(left, openFlowAuxiliaryRows), decodeOpenFlowMeter)
	if errors.Is(err, errOpenFlowUnsupported) {
		out.meterReason = "OPENFLOW_METER_CONFIGURATION_UNSUPPORTED"
		return nil
	}
	if err != nil {
		return err
	}
	out.meterState, out.meterReason = openFlowDescriptionState(limited)
	out.meters = meters
	return nil
}

func openFlowDescriptionState(limited bool) (string, string) {
	if limited {
		return "truncated", "OPENFLOW_CAPTURE_LIMIT"
	}
	return "observed", ""
}

func readOpenFlowDescriptions[T any](r *openFlowReader, xid uint32, kind uint16, limit int, decode func([]byte) (T, bool, error)) ([]T, bool, error) {
	rows := make([]T, 0, limit)
	seen := map[uint32]bool{}
	bytes, detailsLimited, started := openFlowAuxiliaryBytes, false, false
	for {
		body, err := r.reply(ofMultipartReply, xid)
		if errors.Is(err, errOpenFlowWireBudget) {
			return rows, true, nil
		}
		if errors.Is(err, errOpenFlowUnsupported) && started {
			return nil, false, errOpenFlowInvalid
		}
		if err != nil {
			return nil, false, err
		}
		if len(body) < 8 || ofOrder.Uint16(body[:2]) != kind || !openFlowZero(body[4:8]) {
			return nil, false, errOpenFlowInvalid
		}
		started = true
		flags := ofOrder.Uint16(body[2:4])
		if flags&^uint16(ofMultipartReplyMore) != 0 {
			return nil, false, errOpenFlowInvalid
		}
		for data := body[8:]; len(data) != 0; {
			if len(data) < 8 {
				return nil, false, errOpenFlowInvalid
			}
			size := int(ofOrder.Uint16(data[:2]))
			id := ofOrder.Uint32(data[4:8])
			if size < 8 || size%8 != 0 || size > len(data) || seen[id] {
				return nil, false, errOpenFlowInvalid
			}
			row, limited, err := decode(data[:size])
			if err != nil {
				return nil, false, err
			}
			jsonRow, err := json.Marshal(row)
			if err != nil {
				return nil, false, errOpenFlowInvalid
			}
			if len(rows) == limit || len(jsonRow)+1 > bytes {
				return rows, true, nil
			}
			seen[id] = true
			rows = append(rows, row)
			bytes -= len(jsonRow) + 1
			detailsLimited = detailsLimited || limited
			data = data[size:]
		}
		if flags == 0 {
			return rows, detailsLimited, nil
		}
		if len(rows) == limit {
			return rows, true, nil
		}
	}
}

func decodeOpenFlowGroup(data []byte) (OpenFlowGroup, bool, error) {
	if data[3] != 0 {
		return OpenFlowGroup{}, false, errOpenFlowInvalid
	}
	out := OpenFlowGroup{ID: ofOrder.Uint32(data[4:8]), TypeCode: data[2], Buckets: []OpenFlowGroupBucket{}}
	for buckets := data[8:]; len(buckets) > 0; {
		if len(buckets) < 16 || !openFlowZero(buckets[12:16]) {
			return OpenFlowGroup{}, false, errOpenFlowInvalid
		}
		size := int(ofOrder.Uint16(buckets[:2]))
		if size < 16 || size%8 != 0 || size > len(buckets) {
			return OpenFlowGroup{}, false, errOpenFlowInvalid
		}
		row := OpenFlowGroupBucket{Weight: ofOrder.Uint16(buckets[2:4]), ActionTypes: []uint16{}}
		for actions := buckets[16:size]; len(actions) > 0; {
			kind, length, err := openFlowAlignedTLV(actions)
			if err != nil {
				return OpenFlowGroup{}, false, err
			}
			if len(row.ActionTypes) < openFlowMetadata {
				row.ActionTypes = append(row.ActionTypes, kind)
			} else {
				out.MetadataTruncated = true
			}
			actions = actions[length:]
		}
		if len(out.Buckets) < openFlowMetadata {
			out.Buckets = append(out.Buckets, row)
		} else {
			out.MetadataTruncated = true
		}
		buckets = buckets[size:]
	}
	return out, out.MetadataTruncated, nil
}

func decodeOpenFlowMeter(data []byte) (OpenFlowMeter, bool, error) {
	out := OpenFlowMeter{ID: ofOrder.Uint32(data[4:8]), Flags: ofOrder.Uint16(data[2:4]), Bands: []OpenFlowMeterBand{}}
	for bands := data[8:]; len(bands) > 0; {
		kind, size, err := openFlowAlignedTLV(bands)
		if err != nil || size < 16 || (kind == 1 || kind == 2) && size != 16 {
			return OpenFlowMeter{}, false, errOpenFlowInvalid
		}
		if len(out.Bands) < openFlowMetadata {
			out.Bands = append(out.Bands, OpenFlowMeterBand{
				TypeCode: kind, Rate: ofOrder.Uint32(bands[4:8]), Burst: ofOrder.Uint32(bands[8:12]),
			})
		} else {
			out.MetadataTruncated = true
		}
		bands = bands[size:]
	}
	return out, out.MetadataTruncated, nil
}
