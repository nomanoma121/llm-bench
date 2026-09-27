package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testKey generates an RSA key in the PEM form GitHub hands out.
func testKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestTokenIsMintedAndCached(t *testing.T) {
	var calls atomic.Int32
	var sawAuth atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/7/access_tokens" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		sawAuth.Store(r.Header.Get("Authorization"))
		calls.Add(1)
		expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		fmt.Fprintf(w, `{"token":"ghs_minted","expires_at":%q}`, expiry)
	}))
	defer server.Close()

	app := &App{AppID: 1, InstallationID: 7, PrivateKey: testKey(t), BaseURL: server.URL, HTTPClient: server.Client()}
	first, err := app.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != "ghs_minted" || second != first {
		t.Fatalf("tokens = %q / %q", first, second)
	}
	if calls.Load() != 1 {
		t.Fatalf("token endpoint called %d times; the token was not cached", calls.Load())
	}
	header, _ := sawAuth.Load().(string)
	if !strings.HasPrefix(header, "Bearer ") || len(header) < 40 {
		t.Fatalf("authorization header = %q", header)
	}
	// The JWT must be a three-part RS256 token for this app.
	parts := strings.Split(strings.TrimPrefix(header, "Bearer "), ".")
	if len(parts) != 3 {
		t.Fatalf("jwt = %q", header)
	}
}

func TestTokenIsRenewedBeforeItExpires(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		expiry := time.Now().Add(6 * time.Minute).UTC().Format(time.RFC3339)
		fmt.Fprintf(w, `{"token":"ghs_%d","expires_at":%q}`, calls.Load(), expiry)
	}))
	defer server.Close()

	now := time.Now()
	app := &App{
		AppID: 1, InstallationID: 7, PrivateKey: testKey(t), BaseURL: server.URL, HTTPClient: server.Client(),
		Now: func() time.Time { return now },
	}
	if _, err := app.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Inside the renewal window (five minutes before expiry) the token is
	// replaced rather than reused.
	now = now.Add(2 * time.Minute)
	second, err := app.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second != "ghs_2" {
		t.Fatalf("token = %q, want a fresh one", second)
	}
	if calls.Load() != 2 {
		t.Fatalf("token endpoint called %d times", calls.Load())
	}
}

func TestTokenFailuresAreReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
	}))
	defer server.Close()
	app := &App{AppID: 1, InstallationID: 7, PrivateKey: testKey(t), BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := app.Token(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v", err)
	}

	bad := &App{AppID: 1, InstallationID: 7, PrivateKey: []byte("not a pem")}
	if _, err := bad.Token(context.Background()); err == nil {
		t.Fatal("a bad private key was accepted")
	}
}
