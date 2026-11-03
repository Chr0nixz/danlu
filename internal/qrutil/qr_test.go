package qrutil

import (
	"strings"
	"testing"
)

func TestTerminalAndSVG(t *testing.T) {
	url := "https://203.0.113.10:28457/k3x9p2qa7m4z"

	terminal, err := Terminal(url)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(terminal, "█") {
		t.Fatalf("terminal QR is empty: %q", terminal)
	}

	svg, err := SVG(url)
	if err != nil {
		t.Fatal(err)
	}
	text := string(svg)
	if !strings.Contains(text, `viewBox="0 0 `) || !strings.Contains(text, `<path fill="#171717" d="M`) {
		t.Fatalf("unexpected SVG QR output: %s", text[:min(len(text), 160)])
	}
}
