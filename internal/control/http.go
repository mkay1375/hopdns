package control

import (
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	webassets "github.com/mkay1375/hopdns/web"
)

func (c *Controller) Handler() http.Handler {
	slots := make(chan struct{}, 32)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			writeJSON(w, 503, map[string]string{"error": "Panel is busy. Try again shortly."})
			return
		}
		if !trusted(r) {
			writeJSON(w, 403, map[string]string{"error": "Use the local panel and same-origin requests."})
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/config":
			writeJSON(w, 200, c.Config())
		case r.Method == http.MethodGet && r.URL.Path == "/api/status":
			writeJSON(w, 200, c.Status())
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			state := c.Status()["state"].(string)
			code := 503
			if state == "setup_required" || state == "running" {
				code = 200
			}
			writeJSON(w, code, map[string]string{"state": state})
		case r.Method == http.MethodPut && r.URL.Path == "/api/config":
			c.saveHTTP(w, r)
		case r.Method == http.MethodGet:
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			allowed := map[string]string{"index.html": "text/html; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "theme.js": "text/javascript; charset=utf-8", "style.css": "text/css; charset=utf-8"}
			contentType, ok := allowed[path]
			if !ok {
				writeJSON(w, 404, map[string]string{"error": "Not found."})
				return
			}
			body, err := webassets.Files.ReadFile(path)
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": "Panel asset unavailable."})
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.Write(body)
		default:
			writeJSON(w, 405, map[string]string{"error": "Method not allowed."})
		}
	})
}

func trusted(r *http.Request) bool {
	host := r.Host
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	} else if strings.Contains(host, ":") {
		return false
	}
	if name != "localhost" && name != "127.0.0.1" && name != "::1" {
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "http" || u.Host != host || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return false
		}
	}
	return r.Method == http.MethodGet || r.Header.Get("X-HopDNS-Request") == "1"
}

func (c *Controller) saveHTTP(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(w, 415, map[string]string{"error": "Use application/json."})
		return
	}
	if r.ContentLength > 65536 {
		writeJSON(w, 413, map[string]string{"error": "Settings exceed 65,536 bytes."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	value, err := decodeConfig(r.Body)
	if err != nil {
		decodeFailure(w, err)
		return
	}
	if _, err = Validate(value, false); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	restarted, err := c.Apply(value)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Settings could not be saved. Check the data directory permissions and terminal log."})
		return
	}
	writeJSON(w, 200, map[string]any{"config": c.Config(), "restarted": restarted})
}

func decodeFailure(w http.ResponseWriter, err error) {
	var size *http.MaxBytesError
	if errors.As(err, &size) {
		writeJSON(w, 413, map[string]string{"error": "Settings exceed 65,536 bytes."})
	} else {
		writeJSON(w, 400, map[string]string{"error": "Invalid JSON settings."})
	}
}
func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func HTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
}
