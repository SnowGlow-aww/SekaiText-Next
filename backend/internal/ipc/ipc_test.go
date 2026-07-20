package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestFrameRoundTrip checks WriteFrame/ReadFrame are exact inverses, including a
// zero-length body and a binary body containing both NUL bytes and the literal
// "SKF1" magic (must not trip up length-prefixed framing).
func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
		body   []byte
	}{
		{"empty-body", []byte(`{"id":0,"status":204}`), nil},
		{"binary-body", []byte(`{"id":1,"status":200}`), []byte{0x00, 0x01, 'S', 'K', 'F', '1', 0xff, 0x00}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tc.header, tc.body); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			h, b, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if !bytes.Equal(h, tc.header) {
				t.Errorf("header mismatch: got %q want %q", h, tc.header)
			}
			if !bytes.Equal(b, tc.body) {
				t.Errorf("body mismatch: got %v want %v", b, tc.body)
			}
			if buf.Len() != 0 {
				t.Errorf("trailing bytes after frame: %d", buf.Len())
			}
		})
	}
}

func TestReadFrameRejectsOversizeBeforePayloadRead(t *testing.T) {
	tests := []struct {
		name      string
		headerLen uint32
		bodyLen   uint32
	}{
		{"header", maxFrameHeaderSize + 1, 0},
		{"body", 0, maxFrameBodySize + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefix := make([]byte, 12)
			copy(prefix, magic[:])
			binary.LittleEndian.PutUint32(prefix[4:8], tc.headerLen)
			binary.LittleEndian.PutUint32(prefix[8:12], tc.bodyLen)
			_, _, err := ReadFrame(bytes.NewReader(prefix))
			if !errors.Is(err, ErrFrameTooLarge) {
				t.Fatalf("ReadFrame error = %v, want ErrFrameTooLarge", err)
			}
		})
	}
}

func TestReadRequestFrameUsesSmallerBodyLimit(t *testing.T) {
	prefix := make([]byte, 12)
	copy(prefix, magic[:])
	binary.LittleEndian.PutUint32(prefix[8:12], maxRequestBodySize+1)
	_, _, err := readFrame(bytes.NewReader(prefix), maxRequestBodySize)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("readFrame error = %v, want ErrFrameTooLarge", err)
	}
}

func TestValidateFrameLengthsRejectsUint32Truncation(t *testing.T) {
	if err := validateFrameLengths(uint64(^uint32(0))+1, 0); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("validateFrameLengths error = %v, want ErrFrameTooLarge", err)
	}
}

func FuzzFrameRoundTrip(f *testing.F) {
	f.Add([]byte(`{"id":1}`), []byte("body"))
	f.Add([]byte{}, []byte{0, 'S', 'K', 'F', '1', 0xff})
	f.Fuzz(func(t *testing.T, header, body []byte) {
		// Keep fuzz iterations cheap; exact protocol boundaries are covered above.
		if len(header) > 1<<16 || len(body) > 1<<20 {
			t.Skip()
		}
		var framed bytes.Buffer
		if err := WriteFrame(&framed, header, body); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
		gotHeader, gotBody, err := ReadFrame(&framed)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if !bytes.Equal(gotHeader, header) || !bytes.Equal(gotBody, body) {
			t.Fatalf("round trip mismatch")
		}
	})
}

func TestCappedRecorderDoesNotGrowPastLimit(t *testing.T) {
	rec := newCappedRecorder(4)
	if n, err := rec.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if !rec.tooLarge || rec.Body.Len() != 4 {
		t.Fatalf("tooLarge=%v retained=%d, want true/4", rec.tooLarge, rec.Body.Len())
	}
}

func TestResponseRecordersShareMemoryBudget(t *testing.T) {
	budget := &byteBudget{limit: 4}
	first := newBudgetedCappedRecorder(8, budget)
	second := newBudgetedCappedRecorder(8, budget)
	if _, err := first.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if !second.budgetExceeded || second.Body.Len() != 0 {
		t.Fatalf("budgetExceeded=%v retained=%d", second.budgetExceeded, second.Body.Len())
	}
	first.releaseBudget()
	third := newBudgetedCappedRecorder(8, budget)
	if _, err := third.Write([]byte("ok")); err != nil || third.budgetExceeded {
		t.Fatalf("budget was not released: err=%v exceeded=%v", err, third.budgetExceeded)
	}
	third.releaseBudget()
}

func TestServeCancelFrameCancelsRequestContext(t *testing.T) {
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() { os.Stdin, os.Stdout = origStdin, origStdout })

	started := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		w.WriteHeader(499)
	})
	done := make(chan error, 1)
	go func() { done <- Serve(handler) }()

	if _, _, err := ReadFrame(outR); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}
	reqHeader, _ := json.Marshal(RequestHeader{ID: 9, Method: "GET", Path: "/wait"})
	if err := WriteFrame(inW, reqHeader, nil); err != nil {
		t.Fatalf("write request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancelHeader, _ := json.Marshal(RequestHeader{Cancel: 9})
	if err := WriteFrame(inW, cancelHeader, nil); err != nil {
		t.Fatalf("write cancel: %v", err)
	}

	header, _, err := ReadFrame(outR)
	if err != nil {
		t.Fatalf("read canceled response: %v", err)
	}
	var resp ResponseHeader
	if err := json.Unmarshal(header, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ID != 9 || resp.Status != 499 {
		t.Fatalf("response = %+v, want id=9 status=499", resp)
	}

	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop on EOF")
	}
}

// TestServeReadyAndEcho drives Serve end-to-end over real OS pipes: it asserts the
// ready control frame is emitted first, a request frame is routed and answered with
// the correct id/status/headers/body, and stdin EOF triggers a graceful return.
func TestServeReadyAndEcho(t *testing.T) {
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() { os.Stdin, os.Stdout = origStdin, origStdout })

	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Path", r.URL.Path)
		w.Header().Set("X-Echo-Query", r.URL.RawQuery)
		w.Header().Set("X-Echo-CT", r.Header.Get("Content-Type"))
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(207)
		w.Write(b)
	})

	done := make(chan error, 1)
	go func() { done <- Serve(echo) }()

	// ready frame
	h, b, err := ReadFrame(outR)
	if err != nil {
		t.Fatalf("read ready frame: %v", err)
	}
	var ready ResponseHeader
	if err := json.Unmarshal(h, &ready); err != nil {
		t.Fatalf("unmarshal ready header: %v", err)
	}
	if ready.ID != 0 || ready.Status != 204 || ready.Headers["X-Sekai-Ready"] != "1" || len(b) != 0 {
		t.Fatalf("bad ready frame: %+v body=%v", ready, b)
	}

	// request frame
	reqHdr, _ := json.Marshal(RequestHeader{
		ID:      7,
		Method:  "POST",
		Path:    "/api/v1/x",
		Query:   "a=b&c=d",
		Headers: map[string]string{"Content-Type": "application/json"},
	})
	reqBody := []byte{0x00, 'S', 'K', 'F', '1', 0x42}
	if err := WriteFrame(inW, reqHdr, reqBody); err != nil {
		t.Fatalf("write request frame: %v", err)
	}

	// response frame
	h, b, err = ReadFrame(outR)
	if err != nil {
		t.Fatalf("read response frame: %v", err)
	}
	var resp ResponseHeader
	if err := json.Unmarshal(h, &resp); err != nil {
		t.Fatalf("unmarshal response header: %v", err)
	}
	if resp.ID != 7 {
		t.Errorf("id: got %d want 7", resp.ID)
	}
	if resp.Status != 207 {
		t.Errorf("status: got %d want 207", resp.Status)
	}
	if resp.Headers["X-Echo-Path"] != "/api/v1/x" {
		t.Errorf("echo path: got %q", resp.Headers["X-Echo-Path"])
	}
	if resp.Headers["X-Echo-Query"] != "a=b&c=d" {
		t.Errorf("echo query: got %q", resp.Headers["X-Echo-Query"])
	}
	if resp.Headers["X-Echo-Ct"] != "application/json" {
		t.Errorf("echo content-type: got %q", resp.Headers["X-Echo-Ct"])
	}
	if !bytes.Equal(b, reqBody) {
		t.Errorf("echo body: got %v want %v", b, reqBody)
	}

	// EOF → graceful return
	inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error on EOF: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after stdin EOF")
	}
}
