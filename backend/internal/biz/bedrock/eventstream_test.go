package bedrock

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"testing"
)

// buildFrame hand-encodes one AWS event-stream binary frame (string-typed
// headers only — the only type Bedrock actually uses on the wire) so the
// test can round-trip through ReadMessage without needing captured
// real-traffic fixtures.
func buildFrame(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()
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

func TestReadMessageRoundTrip(t *testing.T) {
	payload := []byte(`{"bytes":"eyJ0eXBlIjoiY29udGVudF9ibG9ja19kZWx0YSJ9"}`)
	frame := buildFrame(t, map[string]string{
		":event-type":   "chunk",
		":message-type": "event",
	}, payload)

	msg, err := ReadMessage(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.Headers[":event-type"] != "chunk" {
		t.Fatalf("event-type header = %q", msg.Headers[":event-type"])
	}
	if !bytes.Equal(msg.Payload, payload) {
		t.Fatalf("payload mismatch: got %s want %s", msg.Payload, payload)
	}
}

func TestReadMessageMultipleFramesSequentially(t *testing.T) {
	f1 := buildFrame(t, map[string]string{":event-type": "chunk"}, []byte(`{"n":1}`))
	f2 := buildFrame(t, map[string]string{":event-type": "chunk"}, []byte(`{"n":2}`))
	r := io.MultiReader(bytes.NewReader(f1), bytes.NewReader(f2))

	m1, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("first ReadMessage: %v", err)
	}
	if string(m1.Payload) != `{"n":1}` {
		t.Fatalf("first payload = %s", m1.Payload)
	}
	m2, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("second ReadMessage: %v", err)
	}
	if string(m2.Payload) != `{"n":2}` {
		t.Fatalf("second payload = %s", m2.Payload)
	}
	if _, err := ReadMessage(r); err != io.EOF {
		t.Fatalf("expected io.EOF after last frame, got %v", err)
	}
}

func TestReadMessageDetectsCorruptedCRC(t *testing.T) {
	frame := buildFrame(t, map[string]string{":event-type": "chunk"}, []byte(`{"n":1}`))
	frame[len(frame)-1] ^= 0xFF // flip a bit in the trailing message CRC
	if _, err := ReadMessage(bytes.NewReader(frame)); err != errBadCRC {
		t.Fatalf("expected errBadCRC, got %v", err)
	}
}
