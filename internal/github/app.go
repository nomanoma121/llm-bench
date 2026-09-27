package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	gh "github.com/google/go-github/v89/github"
	"golang.org/x/oauth2"
)

type App struct {
	id             int64
	installationID int64
	key            *rsa.PrivateKey

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewApp(appID, installationID int64, keyFile string) (*App, error) {
	pemBytes, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("github: private key is not PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, err8 := x509.ParsePKCS8PrivateKey(block.Bytes)
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if err8 != nil || !ok {
			return nil, fmt.Errorf("github: private key: %w", err)
		}
		key = rsaKey
	}
	return &App{id: appID, installationID: installationID, key: key}, nil
}

func (a *App) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Until(a.expires) > 10*time.Minute {
		return a.token, nil
	}
	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", a.installationID), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
		Message   string    `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusCreated || out.Token == "" {
		return "", fmt.Errorf("github: installation token: %d %s", resp.StatusCode, out.Message)
	}
	a.token, a.expires = out.Token, out.ExpiresAt
	return a.token, nil
}

func (a *App) Client() (*gh.Client, error) {
	src := oauth2.ReuseTokenSource(nil, tokenSource{a})
	return gh.NewClient(gh.WithHTTPClient(oauth2.NewClient(context.Background(), src)))
}

type tokenSource struct{ app *App }

func (t tokenSource) Token() (*oauth2.Token, error) {
	token, err := t.app.Token(context.Background())
	if err != nil {
		return nil, err
	}
	return &oauth2.Token{AccessToken: token, Expiry: t.app.expires.Add(-5 * time.Minute)}, nil
}

func (a *App) jwt() (string, error) {
	enc := base64.RawURLEncoding.EncodeToString
	now := time.Now()
	claims, _ := json.Marshal(map[string]int64{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.id,
	})
	payload := enc([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc(claims)
	sum := sha256.Sum256([]byte(payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return payload + "." + enc(sig), nil
}
