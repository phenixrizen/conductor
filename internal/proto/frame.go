// Package proto defines the binary framing and JSON messages shared by the
// server, the host agent and the browser (mirrored in web/app/utils/protocol.ts
// and documented in docs/protocol.md).
//
// Every terminal-stream message is a binary frame whose first byte is the frame
// type and whose remainder is the payload.
package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Frame types.
const (
	TypeOutput     byte = 0x01 // owner -> client: raw PTY bytes
	TypeInput      byte = 0x02 // client -> owner: raw keystrokes
	TypeControl    byte = 0x03 // both directions: JSON control message
	TypeScrollback byte = 0x04 // owner -> client: replay bytes before ready
	TypeSignal     byte = 0x05 // viewer <-> server: WebRTC signaling JSON
	TypeFile       byte = 0x06 // owner -> client: file read response
	TypeChunk      byte = 0x07 // data channel only: fragment of a large frame
	TypeRelay      byte = 0x10 // host <-> server: envelope for a relayed viewer
)

// Payload limits per frame type.
const (
	MaxOutput     = 16 << 10
	MaxInput      = 32 << 10
	MaxControl    = 8 << 10
	MaxSignal     = 64 << 10
	MaxFileBytes  = 1 << 20
	MaxFileHeader = 64 << 10
	MaxFrame      = 1 + MaxFileBytes + MaxFileHeader + 12
	ViewerIDLen   = 16
	ProtoVersion  = 1
)

// Errors returned by Decode.
var (
	ErrEmptyFrame   = errors.New("proto: empty frame")
	ErrUnknownType  = errors.New("proto: unknown frame type")
	ErrTooLarge     = errors.New("proto: payload exceeds limit")
	ErrBadEnvelope  = errors.New("proto: malformed relay envelope")
	ErrBadFileFrame = errors.New("proto: malformed file frame")
)

// Frame is a decoded frame. Payload aliases the input slice.
type Frame struct {
	Type    byte
	Payload []byte
}

// Encode builds a frame from a type and payload.
func Encode(t byte, payload []byte) []byte {
	out := make([]byte, 1+len(payload))
	out[0] = t
	copy(out[1:], payload)
	return out
}

// EncodeJSON marshals v and wraps it in a frame of the given type.
func EncodeJSON(t byte, v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Encode(t, b), nil
}

// MustControl encodes a control message, panicking on marshal failure. Control
// messages are internal structs, so a failure is a programming error.
func MustControl(v any) []byte {
	b, err := EncodeJSON(TypeControl, v)
	if err != nil {
		panic(err)
	}
	return b
}

// Decode validates the frame type and payload size limit.
func Decode(b []byte) (Frame, error) {
	if len(b) == 0 {
		return Frame{}, ErrEmptyFrame
	}
	f := Frame{Type: b[0], Payload: b[1:]}
	limit, ok := limitFor(f.Type)
	if !ok {
		return Frame{}, fmt.Errorf("%w: 0x%02x", ErrUnknownType, f.Type)
	}
	if len(f.Payload) > limit {
		return Frame{}, fmt.Errorf("%w: type 0x%02x has %d bytes (limit %d)", ErrTooLarge, f.Type, len(f.Payload), limit)
	}
	return f, nil
}

func limitFor(t byte) (int, bool) {
	switch t {
	case TypeOutput, TypeScrollback:
		return MaxOutput, true
	case TypeInput:
		return MaxInput, true
	case TypeControl:
		return MaxControl, true
	case TypeSignal:
		return MaxSignal, true
	case TypeFile:
		return MaxFileBytes + MaxFileHeader + 12, true
	case TypeChunk:
		return ChunkHeaderLen + ChunkSize, true
	case TypeRelay:
		return ViewerIDLen + 1 + MaxFileBytes + MaxFileHeader + 12, true
	}
	return 0, false
}

// EncodeRelay wraps an inner frame with the 16-byte viewer ID.
func EncodeRelay(viewerID string, inner []byte) ([]byte, error) {
	if len(viewerID) != ViewerIDLen {
		return nil, fmt.Errorf("%w: viewer id must be %d bytes", ErrBadEnvelope, ViewerIDLen)
	}
	out := make([]byte, 1+ViewerIDLen+len(inner))
	out[0] = TypeRelay
	copy(out[1:], viewerID)
	copy(out[1+ViewerIDLen:], inner)
	return out, nil
}

// DecodeRelay splits a relay payload (without the leading type byte) into the
// viewer ID and the decoded inner frame. Only terminal-stream types may be relayed.
func DecodeRelay(payload []byte) (string, Frame, error) {
	if len(payload) < ViewerIDLen+1 {
		return "", Frame{}, ErrBadEnvelope
	}
	viewerID := string(payload[:ViewerIDLen])
	inner, err := Decode(payload[ViewerIDLen:])
	if err != nil {
		return "", Frame{}, err
	}
	switch inner.Type {
	case TypeOutput, TypeInput, TypeControl, TypeScrollback, TypeFile:
	default:
		return "", Frame{}, fmt.Errorf("%w: inner type 0x%02x not relayable", ErrBadEnvelope, inner.Type)
	}
	return viewerID, inner, nil
}

// FileHeader describes a file read response.
type FileHeader struct {
	ReqID     string      `json:"reqId"`
	Path      string      `json:"path"`
	Kind      string      `json:"kind"` // file, dir, error
	Size      int64       `json:"size,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
	Binary    bool        `json:"binary,omitempty"`
	Mime      string      `json:"mime,omitempty"`
	Exists    bool        `json:"exists"`
	Entries   []FileEntry `json:"entries,omitempty"`
	Error     *ErrorInfo  `json:"error,omitempty"`
}

// FileEntry is one directory listing row.
type FileEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

// EncodeFile builds a FILE frame: [8-byte reqId][uint32 headerLen][header][bytes].
// reqId is padded or truncated to 8 bytes; the JSON header carries the full ID.
func EncodeFile(h FileHeader, body []byte) ([]byte, error) {
	hb, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	if len(hb) > MaxFileHeader {
		return nil, fmt.Errorf("%w: header too large", ErrBadFileFrame)
	}
	if len(body) > MaxFileBytes {
		return nil, fmt.Errorf("%w: body too large", ErrBadFileFrame)
	}
	out := make([]byte, 1+8+4+len(hb)+len(body))
	out[0] = TypeFile
	copy(out[1:9], padID(h.ReqID))
	binary.BigEndian.PutUint32(out[9:13], uint32(len(hb)))
	copy(out[13:], hb)
	copy(out[13+len(hb):], body)
	return out, nil
}

// DecodeFile parses a FILE payload (without the leading type byte).
func DecodeFile(payload []byte) (FileHeader, []byte, error) {
	if len(payload) < 12 {
		return FileHeader{}, nil, ErrBadFileFrame
	}
	hl := int(binary.BigEndian.Uint32(payload[8:12]))
	if hl > MaxFileHeader || 12+hl > len(payload) {
		return FileHeader{}, nil, ErrBadFileFrame
	}
	var h FileHeader
	if err := json.Unmarshal(payload[12:12+hl], &h); err != nil {
		return FileHeader{}, nil, fmt.Errorf("%w: %v", ErrBadFileFrame, err)
	}
	return h, payload[12+hl:], nil
}

func padID(id string) []byte {
	b := make([]byte, 8)
	copy(b, id)
	return b
}

// Chunking splits frames larger than a data channel message into TypeChunk
// frames: [uint16 msgId][uint32 total][uint32 offset][data]. Browsers cap
// data channel messages (Chromium at 256 KiB), so anything above ChunkSize is
// fragmented by the host and reassembled by the viewer.
const (
	ChunkSize      = 32 << 10
	ChunkHeaderLen = 10
)

// Chunk splits a frame into TypeChunk frames when it exceeds ChunkSize. Small
// frames are returned unchanged as a single element.
func Chunk(msgID uint16, frame []byte) [][]byte {
	if len(frame) <= ChunkSize {
		return [][]byte{frame}
	}
	total := uint32(len(frame))
	var out [][]byte
	for off := 0; off < len(frame); off += ChunkSize {
		end := min(off+ChunkSize, len(frame))
		c := make([]byte, 1+ChunkHeaderLen+(end-off))
		c[0] = TypeChunk
		binary.BigEndian.PutUint16(c[1:3], msgID)
		binary.BigEndian.PutUint32(c[3:7], total)
		binary.BigEndian.PutUint32(c[7:11], uint32(off))
		copy(c[11:], frame[off:end])
		out = append(out, c)
	}
	return out
}

// ChunkInfo parses a TypeChunk payload (without the type byte).
func ChunkInfo(payload []byte) (msgID uint16, total, offset uint32, data []byte, err error) {
	if len(payload) < ChunkHeaderLen {
		return 0, 0, 0, nil, errors.New("proto: short chunk")
	}
	msgID = binary.BigEndian.Uint16(payload[0:2])
	total = binary.BigEndian.Uint32(payload[2:6])
	offset = binary.BigEndian.Uint32(payload[6:10])
	data = payload[10:]
	if total > MaxFrame || uint64(offset)+uint64(len(data)) > uint64(total) {
		return 0, 0, 0, nil, errors.New("proto: chunk out of range")
	}
	return msgID, total, offset, data, nil
}
