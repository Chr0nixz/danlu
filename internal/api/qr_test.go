package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danlu/internal/config"
)

func TestSettingsQRResponse(t *testing.T) {
	c := config.Default()
	c.Server.PublicIP = "203.0.113.10"
	c.Server.Port = 28457
	c.Server.Entrance = "k3x9p2qa7m4z"
	s := &Server{Cfg: c}
	r := httptest.NewRecorder()
	s.settingsQR(r, httptest.NewRequest(http.MethodGet, "/api/settings/qr.svg", nil))

	if r.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", r.Code, r.Body.String())
	}
	if got := r.Header().Get("Content-Type"); got != "image/svg+xml; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if got := r.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache control = %q", got)
	}
	if !strings.Contains(r.Body.String(), "<svg") || !strings.Contains(r.Body.String(), `viewBox="0 0 `) {
		t.Fatalf("unexpected SVG response: %s", r.Body.String()[:min(r.Body.Len(), 160)])
	}
}
