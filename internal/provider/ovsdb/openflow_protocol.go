package ovsdb

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
)

// OpenFlow 1.3.5 §§6.3.1, 7.3.1 and 7.3.5.2. The only messages this
// observer sends are Hello, Features Request, and Multipart Flow Request.
const (
	ofVersion13          = 4
	ofHello              = 0
	ofError              = 1
	ofFeaturesRequest    = 5
	ofFeaturesReply      = 6
	ofMultipartRequest   = 18
	ofMultipartReply     = 19
	ofMultipartFlow      = 1
	ofMultipartReplyMore = 1
	ofMatchOXM           = 1
	openFlowAllTables    = 0xff
	openFlowWireBytes    = 256 << 10
	openFlowReplyParts   = 12
	openFlowMetadata     = 24
	openFlowRowPrefix    = 48
	openFlowRawFieldMax  = 256
)

var ofOrder = binary.BigEndian

type openFlowFrame struct {
	version, kind byte
	xid           uint32
	body          []byte
}

type openFlowReader struct {
	r      io.Reader
	budget int
	parts  int
}

func (r *openFlowReader) next() (openFlowFrame, error) {
	var header [8]byte
	if r.budget < len(header) {
		return openFlowFrame{}, errOpenFlowWireBudget
	}
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		return openFlowFrame{}, openFlowReadError(err)
	}
	size := int(ofOrder.Uint16(header[2:4]))
	if size < len(header) {
		return openFlowFrame{}, errOpenFlowInvalid
	}
	frame := openFlowFrame{version: header[0], kind: header[1], xid: ofOrder.Uint32(header[4:])}
	if size > r.budget {
		return frame, errOpenFlowWireBudget
	}
	r.budget -= size
	frame.body = make([]byte, size-len(header))
	if _, err := io.ReadFull(r.r, frame.body); err != nil {
		return openFlowFrame{}, openFlowReadError(err)
	}
	return frame, nil
}

func (r *openFlowReader) reply(kind byte, xid uint32) ([]byte, error) {
	if kind == ofMultipartReply {
		if r.parts >= openFlowReplyParts {
			return nil, errOpenFlowWireBudget
		}
		r.parts++
	}
	frame, err := r.next()
	if err != nil {
		if errors.Is(err, errOpenFlowWireBudget) && r.budget >= 8 &&
			(frame.version != ofVersion13 || frame.xid != xid || frame.kind != kind) {
			return nil, errOpenFlowInvalid
		}
		return nil, err
	}
	if frame.version != ofVersion13 || frame.xid != xid {
		return nil, errOpenFlowInvalid
	}
	if frame.kind == ofError {
		if len(frame.body) < 4 {
			return nil, errOpenFlowInvalid
		}
		// OFPET_BAD_REQUEST with BAD_VERSION, BAD_TYPE or BAD_MULTIPART.
		// Other errors, including EPERM, do not establish lack of support.
		if ofOrder.Uint16(frame.body[:2]) == 1 && ofOrder.Uint16(frame.body[2:4]) <= 2 {
			return nil, errOpenFlowUnsupported
		}
		return nil, errOpenFlowRejected
	}
	if frame.kind != kind {
		return nil, errOpenFlowInvalid
	}
	return frame.body, nil
}

func sendOpenFlow(w io.Writer, kind byte, xid uint32, body []byte) error {
	switch kind {
	case ofHello:
		if len(body) != 8 || ofOrder.Uint16(body[:2]) != 1 || ofOrder.Uint16(body[2:4]) != 8 ||
			ofOrder.Uint32(body[4:]) != 1<<ofVersion13 {
			return errOpenFlowInvalid
		}
	case ofFeaturesRequest:
		if len(body) != 0 {
			return errOpenFlowInvalid
		}
	case ofMultipartRequest:
		if !fixedOpenFlowMultipart(body) {
			return errOpenFlowInvalid
		}
	default:
		return errOpenFlowInvalid
	}
	buf := make([]byte, 8+len(body))
	buf[0], buf[1] = ofVersion13, kind
	ofOrder.PutUint16(buf[2:4], uint16(len(buf)))
	ofOrder.PutUint32(buf[4:8], xid)
	copy(buf[8:], body)
	n, err := w.Write(buf)
	if err == nil && n != len(buf) {
		return io.ErrShortWrite
	}
	return err
}

func exchangeOpenFlow(wire io.ReadWriter, expectedDatapath uint64, q openFlowQuery) ([]OpenFlowRow, bool, error) {
	return exchangeOpenFlowWithAuxiliary(wire, expectedDatapath, q, nil)
}

func exchangeOpenFlowWithAuxiliary(wire io.ReadWriter, expectedDatapath uint64, q openFlowQuery, aux *openFlowAuxiliary) ([]OpenFlowRow, bool, error) {
	r := openFlowReader{r: wire, budget: openFlowWireBytes}
	hello := []byte{0, 1, 0, 8, 0, 0, 0, 1 << ofVersion13}
	if err := sendOpenFlow(wire, ofHello, 1, hello); err != nil {
		return nil, false, err
	}
	frame, err := r.next()
	if err != nil {
		return nil, false, err
	}
	if err := negotiateOpenFlow13(frame); err != nil {
		return nil, false, err
	}
	if err := sendOpenFlow(wire, ofFeaturesRequest, 2, nil); err != nil {
		return nil, false, err
	}
	features, err := r.reply(ofFeaturesReply, 2)
	if err != nil {
		return nil, false, err
	}
	if len(features) != 24 || features[12] == 0 || features[13] != 0 || !openFlowZero(features[14:16]) {
		return nil, false, errOpenFlowInvalid
	}
	if ofOrder.Uint64(features[:8]) != expectedDatapath {
		return nil, false, errOpenFlowIdentity
	}
	if err := sendOpenFlow(wire, ofMultipartRequest, 3, openFlowRequest(q)); err != nil {
		return nil, false, err
	}
	rows, limited, err := readOpenFlowRows(&r, q)
	if err == nil && aux != nil {
		err = readOpenFlowAuxiliary(wire, &r, len(rows), limited, aux)
	}
	return rows, limited, err
}

func negotiateOpenFlow13(frame openFlowFrame) error {
	if frame.kind != ofHello || len(frame.body) > 1024 {
		return errOpenFlowInvalid
	}
	if frame.version < ofVersion13 {
		return errOpenFlowUnsupported
	}
	var hasBitmap, supports13 bool
	body := frame.body
	for len(body) > 0 {
		if len(body) < 4 {
			return errOpenFlowInvalid
		}
		kind, size := ofOrder.Uint16(body[:2]), int(ofOrder.Uint16(body[2:4]))
		padded := alignedOpenFlow(size)
		if size < 4 || padded > len(body) || !openFlowZero(body[size:padded]) {
			return errOpenFlowInvalid
		}
		if kind == 1 {
			if hasBitmap || size < 8 || size%4 != 0 {
				return errOpenFlowInvalid
			}
			hasBitmap = true
			supports13 = ofOrder.Uint32(body[4:8])&(1<<ofVersion13) != 0
		}
		body = body[padded:]
	}
	if hasBitmap && supports13 || !hasBitmap && frame.version == ofVersion13 {
		return nil
	}
	return errOpenFlowUnsupported
}

func openFlowRequest(q openFlowQuery) []byte {
	body := make([]byte, 48)
	ofOrder.PutUint16(body[:2], ofMultipartFlow)
	body[8] = q.table
	ofOrder.PutUint32(body[12:16], ^uint32(0)) // OFPP_ANY
	ofOrder.PutUint32(body[16:20], ^uint32(0)) // OFPG_ANY
	if q.hasCookie {
		ofOrder.PutUint64(body[24:32], q.cookie)
		ofOrder.PutUint64(body[32:40], ^uint64(0))
	}
	ofOrder.PutUint16(body[40:42], ofMatchOXM)
	ofOrder.PutUint16(body[42:44], 4) // empty match
	return body
}

func readOpenFlowRows(r *openFlowReader, q openFlowQuery) ([]OpenFlowRow, bool, error) {
	rows := make([]OpenFlowRow, 0, q.limit)
	left := MaxOpenFlowBytes - 2048 // reserve envelope for the captured query/source
	if q.auxiliary {
		left = MaxOpenFlowBytes/2 - 2048 // share the capture budget with groups and meters
	}
	limitedFields := false
	for part := 0; part < openFlowReplyParts; part++ {
		body, err := r.reply(ofMultipartReply, 3)
		if errors.Is(err, errOpenFlowWireBudget) {
			return rows, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		if len(body) < 8 || ofOrder.Uint16(body[:2]) != ofMultipartFlow || !openFlowZero(body[4:8]) {
			return nil, false, errOpenFlowInvalid
		}
		flags := ofOrder.Uint16(body[2:4])
		if flags&^uint16(ofMultipartReplyMore) != 0 {
			return nil, false, errOpenFlowInvalid
		}
		for data := body[8:]; len(data) != 0; {
			row, size, err := decodeOpenFlowRow(data, q.rawFields)
			if err != nil || q.table != openFlowAllTables && row.Table != q.table ||
				q.hasCookie && ofOrder.Uint64(data[24:32]) != q.cookie {
				return nil, false, errOpenFlowInvalid
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				return nil, false, errOpenFlowInvalid
			}
			if len(rows) == q.limit || len(encoded)+1 > left {
				return rows, true, nil
			}
			rows = append(rows, row)
			left -= len(encoded) + 1
			limitedFields = limitedFields || row.MetadataTruncated
			data = data[size:]
		}
		if flags == 0 {
			return rows, limitedFields, nil
		}
		if len(rows) == q.limit {
			return rows, true, nil
		}
	}
	return rows, true, nil
}

func decodeOpenFlowRow(data []byte, rawFields bool) (OpenFlowRow, int, error) {
	var row OpenFlowRow
	if len(data) < openFlowRowPrefix+8 {
		return row, 0, errOpenFlowInvalid
	}
	size := int(ofOrder.Uint16(data[:2]))
	if size < openFlowRowPrefix+8 || size%8 != 0 || size > len(data) ||
		data[2] == openFlowAllTables || data[3] != 0 || !openFlowZero(data[20:24]) ||
		ofOrder.Uint32(data[8:12]) >= 1_000_000_000 {
		return row, 0, errOpenFlowInvalid
	}
	match := data[openFlowRowPrefix:size]
	matchSize := int(ofOrder.Uint16(match[2:4]))
	matchPadded := alignedOpenFlow(matchSize)
	if ofOrder.Uint16(match[:2]) != ofMatchOXM || matchSize < 4 ||
		matchPadded > len(match) || !openFlowZero(match[matchSize:matchPadded]) {
		return row, 0, errOpenFlowInvalid
	}
	fields, truncatedFields, err := decodeOpenFlowMatch(match[4:matchSize], rawFields)
	if err != nil {
		return row, 0, err
	}
	instructions, truncatedInstructions, err := decodeOpenFlowInstructions(match[matchPadded:], rawFields)
	if err != nil {
		return row, 0, err
	}
	return OpenFlowRow{
		Table: data[2], Priority: ofOrder.Uint16(data[12:14]), Cookie: openFlowHex(ofOrder.Uint64(data[24:32])),
		DurationSeconds: ofOrder.Uint32(data[4:8]), DurationNanoseconds: ofOrder.Uint32(data[8:12]),
		Packets: openFlowCounter(ofOrder.Uint64(data[32:40])), Bytes: openFlowCounter(ofOrder.Uint64(data[40:48])),
		Match: fields, Instructions: instructions, MetadataTruncated: truncatedFields || truncatedInstructions,
	}, size, nil
}

func decodeOpenFlowMatch(data []byte, rawFields bool) ([]OpenFlowMatchField, bool, error) {
	fields := []OpenFlowMatchField{}
	truncated := false
	for len(data) > 0 {
		if len(data) < 4 || data[3] == 0 || int(data[3])+4 > len(data) {
			return nil, false, errOpenFlowInvalid
		}
		class, masked, payload := ofOrder.Uint16(data[:2]), data[2]&1 != 0, int(data[3])
		valueBytes := payload
		if class == 0xffff { // experimenter ID prefixes the open payload
			valueBytes -= 4
		}
		if valueBytes <= 0 || masked && valueBytes%2 != 0 {
			return nil, false, errOpenFlowInvalid
		}
		if len(fields) < openFlowMetadata {
			field := OpenFlowMatchField{Class: class, Field: data[2] >> 1, Masked: masked, PayloadBytes: data[3]}
			if rawFields {
				field.RawPayload = hex.EncodeToString(data[4 : 4+payload])
			}
			fields = append(fields, field)
		} else {
			truncated = true
		}
		data = data[4+payload:]
	}
	return fields, truncated, nil
}

func decodeOpenFlowInstructions(data []byte, rawFields bool) ([]OpenFlowInstruction, bool, error) {
	instructions := []OpenFlowInstruction{}
	truncated := false
	for len(data) > 0 {
		kind, size, err := openFlowAlignedTLV(data)
		if err != nil {
			return nil, false, err
		}
		ins := OpenFlowInstruction{Type: kind, Bytes: uint16(size)}
		switch kind {
		case 1, 5, 6:
			if size != 8 {
				return nil, false, errOpenFlowInvalid
			}
		case 2:
			if size != 24 {
				return nil, false, errOpenFlowInvalid
			}
		case 3, 4: // bounded metadata for write/apply-actions
			for actions := data[8:size]; len(actions) > 0; {
				action, length, err := openFlowAlignedTLV(actions)
				if err != nil {
					return nil, false, err
				}
				if len(ins.Actions) < openFlowMetadata {
					ins.Actions = append(ins.Actions, action)
				} else {
					truncated = true
				}
				actions = actions[length:]
			}
		}
		if len(instructions) < openFlowMetadata {
			if rawFields {
				if size-4 <= openFlowRawFieldMax {
					ins.RawPayload = hex.EncodeToString(data[4:size])
				} else {
					truncated = true
				}
			}
			instructions = append(instructions, ins)
		} else {
			truncated = true
		}
		data = data[size:]
	}
	return instructions, truncated, nil
}

func openFlowAlignedTLV(data []byte) (uint16, int, error) {
	if len(data) < 8 {
		return 0, 0, errOpenFlowInvalid
	}
	size := int(ofOrder.Uint16(data[2:4]))
	if size < 8 || size%8 != 0 || size > len(data) {
		return 0, 0, errOpenFlowInvalid
	}
	return ofOrder.Uint16(data[:2]), size, nil
}

func alignedOpenFlow(n int) int { return (n + 7) &^ 7 }

func openFlowZero(data []byte) bool {
	for _, v := range data {
		if v != 0 {
			return false
		}
	}
	return true
}

func openFlowCounter(count uint64) *string {
	if count == ^uint64(0) {
		return nil
	}
	s := strconv.FormatUint(count, 10)
	return &s
}

func openFlowReadError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errOpenFlowInvalid
	}
	return err
}
