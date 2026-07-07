package bedrock

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// Message is one decoded AWS event-stream frame. For Bedrock's
// InvokeModelWithResponseStream, Headers carries ":event-type"/":message-type"
// and Payload is the raw JSON body of that event (already unwrapped from the
// SDK-level {"bytes": base64(...)} envelope by ReadMessage).
type Message struct {
	Headers map[string]string
	Payload []byte
}

var errBadCRC = errors.New("bedrock: event-stream prelude/message CRC mismatch")

// ReadMessage reads exactly one AWS event-stream binary frame from r.
// Returns io.EOF (unwrapped) when r is exhausted cleanly between frames —
// callers should loop "for { msg, err := ReadMessage(r); if err == io.EOF { break } }".
//
// Frame layout (big-endian throughout):
//
//	total length (4B) | headers length (4B) | prelude CRC (4B) |
//	headers (headers length bytes) | payload | message CRC (4B)
func ReadMessage(r io.Reader) (*Message, error) {
	prelude := make([]byte, 12)
	if _, err := io.ReadFull(r, prelude); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("bedrock: truncated event-stream prelude: %w", err)
		}
		return nil, err // clean io.EOF between frames
	}
	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])

	if crc32.ChecksumIEEE(prelude[0:8]) != preludeCRC {
		return nil, errBadCRC
	}
	if totalLen < 16 || uint32(len(prelude)) > totalLen {
		return nil, fmt.Errorf("bedrock: invalid event-stream total length %d", totalLen)
	}

	// remaining = headers + payload + trailing 4-byte message CRC
	remaining := make([]byte, totalLen-12)
	if _, err := io.ReadFull(r, remaining); err != nil {
		return nil, fmt.Errorf("bedrock: truncated event-stream message: %w", err)
	}

	msgCRC := binary.BigEndian.Uint32(remaining[len(remaining)-4:])
	crcInput := append(append([]byte{}, prelude...), remaining[:len(remaining)-4]...)
	if crc32.ChecksumIEEE(crcInput) != msgCRC {
		return nil, errBadCRC
	}

	headerBytes := remaining[:headersLen]
	payload := remaining[headersLen : len(remaining)-4]

	headers, err := decodeHeaders(headerBytes)
	if err != nil {
		return nil, err
	}
	return &Message{Headers: headers, Payload: payload}, nil
}

// header value type IDs, per the AWS event-stream spec.
const (
	hvBoolTrue  = 0
	hvBoolFalse = 1
	hvByte      = 2
	hvShort     = 3
	hvInt       = 4
	hvLong      = 5
	hvByteArray = 6
	hvString    = 7
	hvTimestamp = 8
	hvUUID      = 9
)

func decodeHeaders(b []byte) (map[string]string, error) {
	headers := map[string]string{}
	pos := 0
	for pos < len(b) {
		if pos+1 > len(b) {
			return nil, fmt.Errorf("bedrock: truncated header name length")
		}
		nameLen := int(b[pos])
		pos++
		if pos+nameLen > len(b) {
			return nil, fmt.Errorf("bedrock: truncated header name")
		}
		name := string(b[pos : pos+nameLen])
		pos += nameLen

		if pos+1 > len(b) {
			return nil, fmt.Errorf("bedrock: truncated header value type")
		}
		valType := b[pos]
		pos++

		switch valType {
		case hvBoolTrue:
			headers[name] = "true"
		case hvBoolFalse:
			headers[name] = "false"
		case hvByte:
			if pos+1 > len(b) {
				return nil, fmt.Errorf("bedrock: truncated byte header value")
			}
			headers[name] = fmt.Sprintf("%d", int8(b[pos]))
			pos++
		case hvShort:
			if pos+2 > len(b) {
				return nil, fmt.Errorf("bedrock: truncated short header value")
			}
			headers[name] = fmt.Sprintf("%d", int16(binary.BigEndian.Uint16(b[pos:pos+2])))
			pos += 2
		case hvInt:
			if pos+4 > len(b) {
				return nil, fmt.Errorf("bedrock: truncated int header value")
			}
			headers[name] = fmt.Sprintf("%d", int32(binary.BigEndian.Uint32(b[pos:pos+4])))
			pos += 4
		case hvLong, hvTimestamp:
			if pos+8 > len(b) {
				return nil, fmt.Errorf("bedrock: truncated long/timestamp header value")
			}
			headers[name] = fmt.Sprintf("%d", int64(binary.BigEndian.Uint64(b[pos:pos+8])))
			pos += 8
		case hvByteArray, hvString:
			if pos+2 > len(b) {
				return nil, fmt.Errorf("bedrock: truncated string header length")
			}
			vLen := int(binary.BigEndian.Uint16(b[pos : pos+2]))
			pos += 2
			if pos+vLen > len(b) {
				return nil, fmt.Errorf("bedrock: truncated string header value")
			}
			headers[name] = string(b[pos : pos+vLen])
			pos += vLen
		case hvUUID:
			if pos+16 > len(b) {
				return nil, fmt.Errorf("bedrock: truncated uuid header value")
			}
			pos += 16
			headers[name] = ""
		default:
			return nil, fmt.Errorf("bedrock: unknown event-stream header value type %d", valType)
		}
	}
	return headers, nil
}
