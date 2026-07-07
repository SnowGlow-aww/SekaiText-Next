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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
)

// magic is the 4-byte marker that prefixes every frame.
var magic = [4]byte{'S', 'K', 'F', '1'}

// RequestHeader is the JSON header of a request frame (Rust → Go).
// Field names are pinned by transport-spec §2 and must not change.
type RequestHeader struct {
	ID      uint64            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"` // no leading '?'; empty when absent
	Headers map[string]string `json:"headers"`
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
	prefix := make([]byte, 12+len(header))
	copy(prefix[0:4], magic[:])
	binary.LittleEndian.PutUint32(prefix[4:8], uint32(len(header)))
	binary.LittleEndian.PutUint32(prefix[8:12], uint32(len(body)))
	copy(prefix[12:], header)
	if _, err := w.Write(prefix); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return nil
}

// Serve runs the stdio transport: it writes the ready control frame, then reads
// request frames from os.Stdin and dispatches each (in its own goroutine) through
// router via an httptest recorder, writing the response frame back to stdout.
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

	reader := bufio.NewReader(os.Stdin)
	for {
		header, body, err := ReadFrame(reader)
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
		go dispatch(router, req, body, writeFrame)
	}
}

// dispatch reconstructs an *http.Request from a request frame, runs it through
// the existing chi router via httptest.NewRecorder, and writes the response frame.
func dispatch(router http.Handler, req RequestHeader, body []byte, writeFrame func(header, body []byte) error) {
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
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
		if http.CanonicalHeaderKey(k) == "Host" {
			httpReq.Host = v
		}
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httpReq)

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
	if err := writeFrame(respHeader, rec.Body.Bytes()); err != nil {
		log.Printf("ipc: write response frame for id=%d: %v", req.ID, err)
	}
}
