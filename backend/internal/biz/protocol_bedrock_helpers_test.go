package biz

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"testing"
)

// buildBedrockEventStreamFrame hand-encodes one AWS event-stream binary
// frame (string-typed headers only, the only type Bedrock actually uses on
// the wire) — mirrors bedrock/eventstream_test.go's buildFrame exactly,
// duplicated here since that helper is unexported in a different package.
func buildBedrockEventStreamFrame(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()
	const hvString = 7
	var headerBuf bytes.Buffer
	for name, val := range headers {
		headerBuf.WriteByte(byte(len(name)))
		headerBuf.WriteString(name)
		headerBuf.WriteByte(hvString)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(val)))
		headerBuf.Write(lenBuf[:])
		headerBuf.WriteString(val)
	}
	headerBytes := headerBuf.Bytes()

	totalLen := 12 + len(headerBytes) + len(payload) + 4
	prelude := make([]byte, 12)
	binary.BigEndian.PutUint32(prelude[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(prelude[4:8], uint32(len(headerBytes)))
	binary.BigEndian.PutUint32(prelude[8:12], crc32.ChecksumIEEE(prelude[0:8]))

	body := append(append([]byte{}, headerBytes...), payload...)
	msgCRCInput := append(append([]byte{}, prelude...), body...)
	msgCRC := crc32.ChecksumIEEE(msgCRCInput)

	frame := append(append([]byte{}, prelude...), body...)
	crcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBuf, msgCRC)
	return append(frame, crcBuf...)
}

// buildBedrockChunkFrame wraps chunkJSON in the SDK-level {"bytes":base64(...)}
// envelope every Bedrock InvokeModelWithResponseStream "chunk" frame carries,
// then frames it as one AWS event-stream message.
func buildBedrockChunkFrame(t *testing.T, chunkJSON []byte) []byte {
	t.Helper()
	envelope, err := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString(chunkJSON)})
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return buildBedrockEventStreamFrame(t, map[string]string{":event-type": "chunk", ":message-type": "event"}, envelope)
}

// buildBedrockStreamBody concatenates several chunk frames into one reader,
// simulating a full InvokeModelWithResponseStream body.
func buildBedrockStreamBody(t *testing.T, chunks ...[]byte) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	for _, c := range chunks {
		buf.Write(buildBedrockChunkFrame(t, c))
	}
	return bytes.NewReader(buf.Bytes())
}
