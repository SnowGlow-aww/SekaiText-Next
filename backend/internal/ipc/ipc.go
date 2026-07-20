// Package ipc implements a length-prefixed framing protocol over stdio so the
// Go backend can serve the existing chi router without binding a TCP port.
//
// Wire format (transport-spec §2, identical in both directions):
//
//		MAGIC "SKF1" | headerLen uint32 LE | bodyLen uint32 LE | headerJSON | body
//
//	  - MAGIC is the 4 ASCII bytes "SKF1", present on every frame for resync.
//	  - headerLen / bodyLen are little-endian uint32 byte counts.
//	  - headerJSON is UTF-8 JSON (RequestHeader on stdin, ResponseHeader on stdout).
//	  - body is raw bytes (never base64; binary assets travel verbatim, bodyLen may be 0).
//
// The Tauri shell (Rust) writes request frames to this process' stdin and reads
// response frames from stdout. Because stdout carries the frame stream, NOTHING
// else may write to it: Serve redirects os.Stdout and the log package to stderr.
package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// magic is the 4-byte marker that prefixes every frame.
var magic = [4]byte{'S', 'K', 'F', '1'}

const (
	maxFrameHeaderSize    = 1 << 20   // 1 MiB
	maxRequestBodySize    = 16 << 20  // 16 MiB; requests are JSON/control payloads
	maxFrameBodySize      = 128 << 20 // 128 MiB; responses may contain Live2D assets
	maxBufferedResponses  = 192 << 20 // shared cap across concurrent response recorders
	maxConcurrentRequests = 8
)

// ErrFrameTooLarge is returned before allocating or writing a frame whose
// declared size exceeds the transport limits.
var ErrFrameTooLarge = errors.New("ipc: frame exceeds size limit")

// RequestHeader is the JSON header of a request frame (Rust → Go).
// Field names are pinned by transport-spec §2 and must not change.
type RequestHeader struct {
	ID      uint64            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"` // no leading '?'; empty when absent
	Headers map[string]string `json:"headers"`
	Cancel  uint64            `json:"cancel,omitempty"` // control frame: cancel this request id
}

// ResponseHeader is the JSON header of a response frame (Go → Rust).
// Field names are pinned by transport-spec §2 and must not change.
type ResponseHeader struct {
	ID      uint64            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

// ReadFrame reads exactly one frame from r, returning the raw headerJSON bytes
// and the body bytes. A clean EOF at a frame boundary surfaces as io.EOF; a pipe
// closed mid-frame surfaces as io.ErrUnexpectedEOF. The stream stays byte-aligned
// even if headerJSON later fails to parse, because the full frame is consumed here.
func ReadFrame(r io.Reader) (header []byte, body []byte, err error) {
	return readFrame(r, maxFrameBodySize)
}

func readFrame(r io.Reader, maxBodySize uint64) (header []byte, body []byte, err error) {
	var m [4]byte
	if _, err = io.ReadFull(r, m[:]); err != nil {
		return nil, nil, err
	}
	if m != magic {
		return nil, nil, fmt.Errorf("ipc: bad frame magic %q", m[:])
	}
	var lens [8]byte
	if _, err = io.ReadFull(r, lens[:]); err != nil {
		return nil, nil, err
	}
	headerLen := binary.LittleEndian.Uint32(lens[0:4])
	bodyLen := binary.LittleEndian.Uint32(lens[4:8])
	if err := validateFrameLengthsWithBodyLimit(uint64(headerLen), uint64(bodyLen), maxBodySize); err != nil {
		return nil, nil, err
	}

	header = make([]byte, headerLen)
	if _, err = io.ReadFull(r, header); err != nil {
		return nil, nil, err
	}
	body = make([]byte, bodyLen)
	if bodyLen > 0 {
		if _, err = io.ReadFull(r, body); err != nil {
			return nil, nil, err
		}
	}
	return header, body, nil
}

// WriteFrame writes one frame to w. The caller is responsible for serializing
// concurrent writes (Serve guards w with a mutex). MAGIC + the two length words
// + headerJSON go out in a single write; the (possibly large, binary) body in a
// second write so it is never copied.
func WriteFrame(w io.Writer, header []byte, body []byte) error {
	if err := validateFrameLengths(uint64(len(header)), uint64(len(body))); err != nil {
		return err
	}
	prefix := make([]byte, 12+len(header))
	copy(prefix[0:4], magic[:])
	binary.LittleEndian.PutUint32(prefix[4:8], uint32(len(header)))
	binary.LittleEndian.PutUint32(prefix[8:12], uint32(len(body)))
	copy(prefix[12:], header)
	if err := writeAll(w, prefix); err != nil {
		return err
	}
	if len(body) > 0 {
		if err := writeAll(w, body); err != nil {
			return err
		}
	}
	return nil
}

func validateFrameLengths(headerLen, bodyLen uint64) error {
	return validateFrameLengthsWithBodyLimit(headerLen, bodyLen, maxFrameBodySize)
}

func validateFrameLengthsWithBodyLimit(headerLen, bodyLen, maxBodySize uint64) error {
	if headerLen > uint64(^uint32(0)) || bodyLen > uint64(^uint32(0)) {
		return fmt.Errorf("%w: length does not fit uint32", ErrFrameTooLarge)
	}
	if headerLen > maxFrameHeaderSize {
		return fmt.Errorf("%w: header is %d bytes (max %d)", ErrFrameTooLarge, headerLen, maxFrameHeaderSize)
	}
	if bodyLen > maxBodySize {
		return fmt.Errorf("%w: body is %d bytes (max %d)", ErrFrameTooLarge, bodyLen, maxBodySize)
	}
	return nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

// Serve runs the stdio transport: it writes the ready control frame, then reads
// request frames from os.Stdin and dispatches each (in its own goroutine) through
// router via a bounded recorder, writing the response frame back to stdout.
//
// stdout carries response frames ONLY. Serve captures the real stdout up front,
// then points os.Stdout and the standard logger at stderr so any stray
// fmt.Print/log in the request path (e.g. the request-logger middleware) lands on
// stderr instead of corrupting the frame stream. Returns nil on stdin EOF (the
// Rust side closed the pipe) for a clean, orphan-free shutdown.
func Serve(router http.Handler) error {
	// Capture fd 1 for frames, then divert everything else to stderr.
	realStdout := os.Stdout
	os.Stdout = os.Stderr
	log.SetOutput(os.Stderr)
	// A response-frame write that races the Rust side closing the stdout read end
	// hits a broken pipe on fd 1; its default SIGPIPE disposition would terminate the
	// process by signal. Ignore it so the write just returns EPIPE (WriteFrame logs
	// it) and the intended "stdin EOF -> graceful return" path stays in control.
	ignoreSIGPIPE()

	var writeMu sync.Mutex
	writeFrame := func(header, body []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return WriteFrame(realStdout, header, body)
	}

	// ready control frame: id=0, status=204, X-Sekai-Ready:1, empty body.
	readyHeader, err := json.Marshal(ResponseHeader{
		ID:      0,
		Status:  http.StatusNoContent,
		Headers: map[string]string{"X-Sekai-Ready": "1"},
	})
	if err != nil {
		return err
	}
	if err := writeFrame(readyHeader, nil); err != nil {
		return err
	}

	requestSlots := make(chan struct{}, maxConcurrentRequests)
	responseBudget := &byteBudget{limit: maxBufferedResponses}
	var activeMu sync.Mutex
	var activeWG sync.WaitGroup
	active := make(map[uint64]context.CancelFunc)
	cancelAll := func() {
		activeMu.Lock()
		for id, cancel := range active {
			cancel()
			delete(active, id)
		}
		activeMu.Unlock()
	}
	defer func() {
		cancelAll()
		done := make(chan struct{})
		go func() {
			activeWG.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			log.Printf("ipc: timed out waiting for active request handlers during shutdown")
		}
	}()

	writeError := func(id uint64, status int, message string) error {
		h, err := json.Marshal(ResponseHeader{ID: id, Status: status, Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"}})
		if err != nil {
			return err
		}
		return writeFrame(h, []byte(message))
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		header, body, err := readFrame(reader, maxRequestBodySize)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil // pipe closed → graceful exit
			}
			return err
		}
		var req RequestHeader
		if err := json.Unmarshal(header, &req); err != nil {
			// Frame was consumed, so the stream stays aligned; we just cannot
			// route a request we can't parse (and have no reliable id to answer).
			log.Printf("ipc: malformed request header: %v", err)
			continue
		}
		if req.Cancel != 0 {
			activeMu.Lock()
			cancel := active[req.Cancel]
			activeMu.Unlock()
			if cancel != nil {
				cancel()
			}
			continue
		}
		if req.ID == 0 {
			if err := writeError(0, http.StatusBadRequest, "request id 0 is reserved"); err != nil {
				return err
			}
			continue
		}

		select {
		case requestSlots <- struct{}{}:
		default:
			if err := writeError(req.ID, http.StatusServiceUnavailable, "ipc request limit reached"); err != nil {
				return err
			}
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		activeMu.Lock()
		if _, duplicate := active[req.ID]; duplicate {
			activeMu.Unlock()
			cancel()
			<-requestSlots
			if err := writeError(req.ID, http.StatusConflict, "duplicate ipc request id"); err != nil {
				return err
			}
			continue
		}
		active[req.ID] = cancel
		activeMu.Unlock()

		activeWG.Add(1)
		go func() {
			defer func() {
				activeWG.Done()
				activeMu.Lock()
				delete(active, req.ID)
				activeMu.Unlock()
				cancel()
				<-requestSlots
			}()
			dispatch(ctx, router, req, body, writeFrame, responseBudget)
		}()
	}
}

// dispatch reconstructs an *http.Request from a request frame, runs it through
// the existing chi router, and writes the response frame.
func dispatch(ctx context.Context, router http.Handler, req RequestHeader, body []byte, writeFrame func(header, body []byte) error, budget *byteBudget) {
	target := req.Path
	if req.Query != "" {
		target += "?" + req.Query
	}
	u, err := url.ParseRequestURI(target)
	if err != nil {
		// Keep a malformed path routable (router will 404) instead of dropping it.
		u = &url.URL{Path: req.Path, RawQuery: req.Query}
	}

	httpReq := &http.Request{
		Method:        req.Method,
		URL:           u,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header, len(req.Headers)),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Host:          "ipc",
		RemoteAddr:    "ipc",
		RequestURI:    target,
	}
	httpReq = httpReq.WithContext(ctx)
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
		if http.CanonicalHeaderKey(k) == "Host" {
			httpReq.Host = v
		}
	}

	rec := newBudgetedCappedRecorder(maxFrameBodySize, budget)
	defer func() { rec.releaseBudget() }()
	router.ServeHTTP(rec, httpReq)
	if rec.tooLarge || rec.budgetExceeded {
		rec.releaseBudget()
		rec = newCappedRecorder(maxFrameBodySize)
		rec.Header().Set("Content-Type", "text/plain; charset=utf-8")
		rec.WriteHeader(http.StatusInsufficientStorage)
		_, _ = rec.Write([]byte("ipc response body exceeds memory or frame limit"))
	}

	respHeaders := make(map[string]string, len(rec.Header()))
	for k, vs := range rec.Header() {
		// A handler or middleware can emit the same header more than once (e.g.
		// rs/cors adds three Vary values on a preflight). The map[string]string
		// wire model holds one value per key, so fold repeats into a single
		// comma-separated value per RFC 7230 §3.2.2 instead of keeping only the
		// first via Header().Get. (Set-Cookie cannot be comma-folded, but this
		// local app sets no cookies; the frame model would need map[string][]string
		// to carry those, which is a transport-spec change out of scope here.)
		respHeaders[k] = strings.Join(vs, ", ")
	}
	// httptest.ResponseRecorder does NOT sniff Content-Type the way net/http's real
	// server does on first Write. Mirror that sniffing so a handler that wrote a body
	// without an explicit type (e.g. the live2d proxy relaying a CDN asset whose
	// upstream omitted Content-Type) still carries one over the frame transport.
	if respHeaders["Content-Type"] == "" && rec.Body.Len() > 0 {
		b := rec.Body.Bytes()
		if len(b) > 512 {
			b = b[:512]
		}
		respHeaders["Content-Type"] = http.DetectContentType(b)
	}
	respHeader, err := json.Marshal(ResponseHeader{
		ID:      req.ID,
		Status:  rec.Code,
		Headers: respHeaders,
	})
	if err != nil {
		log.Printf("ipc: marshal response header for id=%d: %v", req.ID, err)
		return
	}
	if err := validateFrameLengths(uint64(len(respHeader)), uint64(rec.Body.Len())); err != nil {
		respHeader, _ = json.Marshal(ResponseHeader{
			ID:      req.ID,
			Status:  http.StatusInsufficientStorage,
			Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		})
		rec.releaseBudget()
		rec = newCappedRecorder(maxFrameBodySize)
		_, _ = rec.Write([]byte("ipc response exceeds frame limit"))
	}
	if err := writeFrame(respHeader, rec.Body.Bytes()); err != nil {
		log.Printf("ipc: write response frame for id=%d: %v", req.ID, err)
	}
}

// cappedRecorder mirrors the ResponseRecorder behavior used by the IPC adapter,
// but never retains more than the SKF1 body limit.
type cappedRecorder struct {
	header         http.Header
	Body           bytes.Buffer
	Code           int
	limit          int
	wroteHeader    bool
	tooLarge       bool
	budget         *byteBudget
	reserved       int
	budgetExceeded bool
}

func newCappedRecorder(limit int) *cappedRecorder {
	return &cappedRecorder{header: make(http.Header), Code: http.StatusOK, limit: limit}
}

func newBudgetedCappedRecorder(limit int, budget *byteBudget) *cappedRecorder {
	recorder := newCappedRecorder(limit)
	recorder.budget = budget
	return recorder
}

func (r *cappedRecorder) Header() http.Header { return r.header }

func (r *cappedRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.Code = code
}

func (r *cappedRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	remaining := r.limit - r.Body.Len()
	retained := len(p)
	if retained > remaining {
		retained = remaining
	}
	if retained > 0 && r.budget != nil && !r.budget.reserve(retained) {
		r.budgetExceeded = true
		return len(p), nil
	}
	r.reserved += retained
	if len(p) > remaining {
		if remaining > 0 {
			_, _ = r.Body.Write(p[:remaining])
		}
		r.tooLarge = true
		return len(p), nil
	}
	_, _ = r.Body.Write(p)
	return len(p), nil
}

func (r *cappedRecorder) releaseBudget() {
	if r.budget != nil && r.reserved > 0 {
		r.budget.release(r.reserved)
		r.reserved = 0
	}
}

type byteBudget struct {
	mu    sync.Mutex
	used  int
	limit int
}

func (b *byteBudget) reserve(size int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if size < 0 || b.used > b.limit-size {
		return false
	}
	b.used += size
	return true
}

func (b *byteBudget) release(size int) {
	b.mu.Lock()
	b.used -= size
	if b.used < 0 {
		b.used = 0
	}
	b.mu.Unlock()
}

func (r *cappedRecorder) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
}
