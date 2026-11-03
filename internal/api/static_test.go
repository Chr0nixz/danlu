package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAUsesPrecompressedAssetWhenAccepted(t *testing.T) {
	const source = "const danlu = 'the training dashboard asset';"
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte(source)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Server{Static: fstest.MapFS{
		"assets/app.js":    &fstest.MapFile{Data: []byte(source)},
		"assets/app.js.gz": &fstest.MapFile{Data: compressed.Bytes()},
		"index.html":       &fstest.MapFile{Data: []byte("<html></html>")},
	}}
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	res := httptest.NewRecorder()
	s.spa().ServeHTTP(res, req)
	if res.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(res.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("unexpected headers: %v", res.Header())
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(zr)
	if err != nil || string(decoded) != source {
		t.Fatalf("decoded asset = %q, %v", decoded, err)
	}
}

func TestAcceptsGzipHonorsQuality(t *testing.T) {
	for header, want := range map[string]bool{
		"gzip": true, "br, gzip;q=0.5": true, "gzip;q=0, *;q=1": false, "br": false, "*;q=0.4": true,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
