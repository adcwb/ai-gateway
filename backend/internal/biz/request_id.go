package biz

import (
	"crypto/rand"
	"encoding/binary"
	"strconv"
	"sync/atomic"
)

var requestIDPrefix = func() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "r0"
	}
	n := binary.BigEndian.Uint64(b)
	return "r" + strconv.FormatUint(n, 36)
}()

var requestIDCounter atomic.Uint64

// GenerateRequestID generates a unique request ID for concurrency slot tracking.
func GenerateRequestID() string { return generateRequestID() }

func generateRequestID() string {
	seq := requestIDCounter.Add(1)
	return requestIDPrefix + "-" + strconv.FormatUint(seq, 36)
}
