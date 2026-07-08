package extension

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// minimalWasmModule is a hand-assembled WASM binary (no toolchain available
// offline in this environment — see the package doc comment on WasmHook)
// implementing just enough of the ABI to exercise the host-side plumbing:
//
//   - exports memory (1 page)
//   - exports alloc(size i32) -> i32: ignores size, always returns 1000
//     (a fixed scratch offset — fine for a single sequential call per test)
//   - exports handle(ptr i32, len i32) -> i64: ignores its params entirely
//     and always returns packed = (0<<32 | 17), pointing at a data segment
//     at offset 0 containing the 17-byte string `{"action":"pass"}`
//
// This deliberately doesn't parse the event JSON a real guest would — it's
// only here to prove alloc/Write/handle/Read/unmarshal round-trip correctly
// through wazero, not to be a realistic guest implementation.
var minimalWasmModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, // magic + version

	// type section: type0 (i32)->(i32), type1 (i32,i32)->(i64)
	0x01, 0x0C, 0x02,
	0x60, 0x01, 0x7F, 0x01, 0x7F,
	0x60, 0x02, 0x7F, 0x7F, 0x01, 0x7E,

	// function section: func0 uses type0 (alloc), func1 uses type1 (handle)
	0x03, 0x03, 0x02, 0x00, 0x01,

	// memory section: 1 memory, min 1 page
	0x05, 0x03, 0x01, 0x00, 0x01,

	// export section: memory, alloc (func0), handle (func1)
	0x07, 0x1B, 0x03,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x05, 'a', 'l', 'l', 'o', 'c', 0x00, 0x00,
	0x06, 'h', 'a', 'n', 'd', 'l', 'e', 0x00, 0x01,

	// code section: func0 body "i32.const 1000", func1 body "i64.const 17"
	0x0A, 0x0C, 0x02,
	0x05, 0x00, 0x41, 0xE8, 0x07, 0x0B, // alloc: locals=0, i32.const 1000, end
	0x04, 0x00, 0x42, 0x11, 0x0B, // handle: locals=0, i64.const 17, end

	// data section: active segment at offset 0, 17 bytes `{"action":"pass"}`
	0x0B, 0x17, 0x01,
	0x00, 0x41, 0x00, 0x0B, 0x11,
	'{', '"', 'a', 'c', 't', 'i', 'o', 'n', '"', ':', '"', 'p', 'a', 's', 's', '"', '}',
}

func writeTestWasmModule(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.wasm")
	if err := os.WriteFile(path, minimalWasmModule, 0o644); err != nil {
		t.Fatalf("write wasm fixture: %v", err)
	}
	return path
}

func TestWasmHook_HandleRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := writeTestWasmModule(t)

	h, err := NewWasmHook(ctx, "test-wasm", path)
	if err != nil {
		t.Fatalf("NewWasmHook: %v", err)
	}
	defer h.Close(ctx)

	res, err := h.Handle(ctx, Event{Point: PreRequest, IR: []byte(`{"anything":true}`)})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Action != ActionPass {
		t.Fatalf("expected the guest's fixed {\"action\":\"pass\"} response to decode to ActionPass, got %+v", res)
	}
}

func TestWasmHook_MissingModuleErrors(t *testing.T) {
	if _, err := NewWasmHook(context.Background(), "missing", filepath.Join(t.TempDir(), "does-not-exist.wasm")); err == nil {
		t.Fatal("expected an error for a missing wasm file")
	}
}

func TestWasmHook_MultipleCallsGetIsolatedInstances(t *testing.T) {
	ctx := context.Background()
	path := writeTestWasmModule(t)

	h, err := NewWasmHook(ctx, "test-wasm", path)
	if err != nil {
		t.Fatalf("NewWasmHook: %v", err)
	}
	defer h.Close(ctx)

	for i := 0; i < 3; i++ {
		if _, err := h.Handle(ctx, Event{IR: []byte(`{}`)}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}
