package web

import (
	"bufio"
	"compress/gzip"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

// Response compression for everything the handlers write: pages, htmx
// partials, JSON exports, error pages. Static files are compressed in advance
// (static.go) and set their own Content-Encoding, so they pass through.
//
// Responses are compressed on the fly, so the levels favour speed: brotli 5
// and gzip 6 compress a 50 KB page in well under a millisecond each, while
// getting close to the best ratio for HTML. Brotli goes to browsers that
// accept it (over HTTPS or on localhost), gzip to the rest.
const (
	compressMinSize     = 1024 // smaller bodies gain little and cost a frame
	compressBrotliLevel = 5
	compressGzipLevel   = gzip.DefaultCompression
)

var (
	brotliPool = sync.Pool{New: func() any { return brotli.NewWriterLevel(io.Discard, compressBrotliLevel) }}
	gzipPool   = sync.Pool{New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, compressGzipLevel)
		return w
	}}
)

// compressResponses compresses responses for clients that accept br or gzip.
func compressResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := ""
		switch {
		case r.Method == http.MethodHead:
		case accepts(r, "br"):
			enc = "br"
		case accepts(r, "gzip"):
			enc = "gzip"
		}
		cw := &compressWriter{ResponseWriter: w, enc: enc, status: http.StatusOK}
		defer cw.Close()
		next.ServeHTTP(cw, r)
	})
}

// compressWriter holds back the first compressMinSize bytes so it can tell
// small responses from large ones, then decides once whether to compress.
type compressWriter struct {
	http.ResponseWriter
	enc         string // negotiated encoding, "" for none
	status      int
	wroteHeader bool // WriteHeader called by the handler
	decided     bool // headers sent to the client
	buf         []byte
	zw          interface {
		io.WriteCloser
		Flush() error
	}
}

func (c *compressWriter) WriteHeader(code int) {
	if c.wroteHeader || c.decided {
		return
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		c.ResponseWriter.WriteHeader(code) // informational (103 Early Hints): pass on
		return
	}
	c.wroteHeader = true
	c.status = code
	if !bodyAllowed(code) {
		c.decide(false)
	}
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.decided {
		if !bodyAllowed(c.status) {
			c.decide(false)
		} else if n := len(c.buf) + len(p); n < compressMinSize && c.Header().Get("Content-Length") == "" {
			c.buf = append(c.buf, p...)
			return len(p), nil
		} else {
			c.sniff(p)
			c.decide(n >= compressMinSize)
		}
	}
	if c.zw != nil {
		return c.zw.Write(p)
	}
	return c.ResponseWriter.Write(p)
}

// decide sends the headers, compressing when the body is big enough (large)
// and its type and headers allow it, then writes out what was held back.
func (c *compressWriter) decide(large bool) {
	c.decided = true
	c.sniff(nil)
	h := c.Header()
	if c.eligible() {
		h.Add("Vary", "Accept-Encoding")
		if large && c.enc != "" {
			h.Set("Content-Encoding", c.enc)
			h.Del("Content-Length")
			if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
				h.Set("ETag", "W/"+etag) // the bytes differ from the identity body
			}
			switch c.enc {
			case "br":
				bw := brotliPool.Get().(*brotli.Writer)
				bw.Reset(c.ResponseWriter)
				c.zw = bw
			case "gzip":
				gw := gzipPool.Get().(*gzip.Writer)
				gw.Reset(c.ResponseWriter)
				c.zw = gw
			}
		}
	}
	c.ResponseWriter.WriteHeader(c.status)
	if len(c.buf) > 0 {
		buf := c.buf
		c.buf = nil
		if c.zw != nil {
			c.zw.Write(buf)
		} else {
			c.ResponseWriter.Write(buf)
		}
	}
}

// sniff sets a missing Content-Type from the start of the body, as net/http
// would, so the type check below sees it.
func (c *compressWriter) sniff(next []byte) {
	h := c.Header()
	if _, set := h["Content-Type"]; set || len(c.buf)+len(next) == 0 {
		return
	}
	h.Set("Content-Type", http.DetectContentType(append(c.buf[:len(c.buf):len(c.buf)], next...)))
}

// eligible reports whether the response may be compressed at all.
func (c *compressWriter) eligible() bool {
	h := c.Header()
	if !bodyAllowed(c.status) || h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" {
		return false
	}
	if strings.Contains(h.Get("Cache-Control"), "no-transform") {
		return false
	}
	return compressibleType(h.Get("Content-Type"))
}

// Close finishes the response: it sends a small body as is, or ends the
// compressed stream. Called once the handler has returned.
func (c *compressWriter) Close() error {
	if !c.decided {
		if !c.wroteHeader && len(c.buf) == 0 {
			return nil // nothing written: let the server send its default (200, empty)
		}
		c.decide(false)
	}
	if c.zw == nil {
		return nil
	}
	err := c.zw.Close()
	switch zw := c.zw.(type) {
	case *brotli.Writer:
		zw.Reset(io.Discard)
		brotliPool.Put(zw)
	case *gzip.Writer:
		zw.Reset(io.Discard)
		gzipPool.Put(zw)
	}
	c.zw = nil
	return err
}

// Flush sends what has been written so far (streaming responses).
func (c *compressWriter) Flush() {
	if !c.decided {
		c.decide(len(c.buf) > 0)
	}
	if c.zw != nil {
		c.zw.Flush()
	}
	http.NewResponseController(c.ResponseWriter).Flush()
}

// Hijack passes through for protocol upgrades (none today, but harmless).
func (c *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(c.ResponseWriter).Hijack()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified
}

// compressibleType reports whether a Content-Type is text-like.
func compressibleType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	if strings.HasPrefix(mt, "text/") {
		return true
	}
	switch mt {
	case "application/json", "application/javascript", "application/xml",
		"application/manifest+json", "application/problem+json", "image/svg+xml":
		return true
	}
	return strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml")
}
