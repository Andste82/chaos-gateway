package api

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// uiCSP is the content security policy of the web app (plan §2.16): everything from the gateway
// itself. Inline style attributes are allowed because Vue binds styles (bar widths, chart sizes)
// as attributes; scripts are never inline.
const uiCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// serveUI answers a GET or HEAD outside /api/ from the web app's build output (plan §3.7: the
// single-page app is embedded into the binary). A file of the build is sent as it is; every other
// path is a route of the app and gets index.html, so a reload of /devices/esp32-42 works. Files
// under assets/ carry a content hash in their name and may be cached for good; index.html never.
// It reports false when the request is not one for the app.
func (s *Server) serveUI(c *gin.Context) bool {
	ui := s.cfg.UI
	if ui == nil {
		return false
	}
	method, p := c.Request.Method, c.Request.URL.Path
	if (method != http.MethodGet && method != http.MethodHead) || p == "/api" || strings.HasPrefix(p, "/api/") {
		return false
	}
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	h := c.Writer.Header()
	if f, err := ui.Open(name); err == nil {
		st, err := f.Stat()
		if err == nil && !st.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				h.Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			if name == "index.html" {
				h.Set("Content-Security-Policy", uiCSP)
			}
			serveFile(c, name, f, st.ModTime())
			return true
		}
		_ = f.Close()
	}
	// a missing build file is a 404, not the app: a stale page asking for an old chunk must fail
	// visibly instead of parsing index.html as JavaScript
	if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
		return false
	}
	f, err := ui.Open("index.html")
	if err != nil {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return false
	}
	h.Set("Content-Security-Policy", uiCSP)
	serveFile(c, "index.html", f, st.ModTime())
	return true
}

// serveFile sends a file of the build with the content type of its name.
func serveFile(c *gin.Context, name string, f fs.File, mod time.Time) {
	defer func() { _ = f.Close() }()
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(c.Writer, c.Request, name, mod, rs)
		return
	}
	b, err := io.ReadAll(f)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	http.ServeContent(c.Writer, c.Request, name, mod, bytes.NewReader(b))
}

// CheckUI reports whether fsys holds a build of the web app.
func CheckUI(fsys fs.FS) error {
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return errors.New("no index.html: build the web app with `make build-web`")
	}
	return nil
}
