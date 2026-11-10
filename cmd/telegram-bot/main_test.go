package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/url"
	"strings"
	"testing"
)

func TestNetworkErrorLogRedactsToken(t *testing.T) {
	const token = "123456789:test-secret"
	var output bytes.Buffer
	logger := log.New(tokenRedactingWriter{writer: &output, token: token}, "", 0)
	for _, method := range []string{"getMe", "getUpdates", "sendMessage"} {
		err := &url.Error{Op: "Post", URL: "https://api.telegram.org/bot" + token + "/" + method, Err: errors.New("timeout")}
		logger.Printf("Telegram: %v", err)
	}
	if strings.Contains(output.String(), token) {
		t.Fatal("network error leaked the bot token")
	}
	if strings.Count(output.String(), "[REDACTED_TOKEN]") != 3 || !strings.Contains(output.String(), "getUpdates") || !strings.Contains(output.String(), "timeout") {
		t.Fatalf("missing redaction or useful diagnostics: %s", output.String())
	}
}

func TestTokenRedactingWriterCount(t *testing.T) {
	var output bytes.Buffer
	p := []byte("bot123:secret/getMe")
	n, err := (tokenRedactingWriter{writer: &output, token: "123:secret"}).Write(p)
	if n != len(p) || err != nil || output.String() != "bot[REDACTED_TOKEN]/getMe" {
		t.Fatalf("write: n=%d, err=%v, output=%q", n, err, output.String())
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return 0, nil }

func TestTokenRedactingWriterReportsShortWrite(t *testing.T) {
	_, err := (tokenRedactingWriter{writer: shortWriter{}, token: "secret"}).Write([]byte("secret"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("got %v, want short write", err)
	}
}
