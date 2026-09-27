// Package githubapp authenticates as a GitHub App installation.
//
// The App private key never leaves the controller: the sandbox receives only a
// short-lived installation token, and this package is the only place that
// mints one. Installation tokens last an hour, so they are cached and renewed
// shortly before they expire instead of being requested per API call.
package githubapp

import (
	"bytes"
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
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v89/github"
	"golang.org/x/oauth2"
)

// DefaultBaseURL is the public GitHub API endpoint.
const DefaultBaseURL = "https://api.github.com"

// tokenLifetime is how long a minted JWT is valid. GitHub rejects a JWT that
// lives longer than ten minutes.
const tokenLifetime = 9 * time.Minute

// renewBefore is how early a cached installation token is replaced.
const renewBefore = 5 * time.Minute

// App is one GitHub App installation.
type App struct {
	AppID          int64
	InstallationID int64
	// PrivateKey is the PEM-encoded RSA private key of the App.
	PrivateKey []byte
	// BaseURL overrides the API endpoint, for GitHub Enterprise.
	BaseURL string
	// HTTPClient is used for the token request; tests substitute one.
	HTTPClient *http.Client
	// Now is the clock, so tests can expire a token.
	Now func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewFromFiles builds an App from key files on disk.
func NewFromFiles(appID, installationID int64, privateKeyFile, baseURL string) (*App, error) {
	key, err := os.ReadFile(privateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("githubapp: read private key: %w", err)
	}
	return &App{AppID: appID, InstallationID: installationID, PrivateKey: key, BaseURL: baseURL}, nil
}

func (a *App) baseURL() string {
	if a.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(a.BaseURL, "/")
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) client() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return http.DefaultClient
}

// Token returns a valid installation token, minting one when the cached token
// is missing or about to expire.
func (a *App) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && a.now().Add(renewBefore).Before(a.expires) {
		return a.token, nil
	}
	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	token, expires, err := a.mint(ctx, jwt)
	if err != nil {
		return "", err
	}
	a.token, a.expires = token, expires
	return token, nil
}

// TokenOptions narrow an installation token. GitHub grants an unrestricted
// token when the body is empty, which would give a sandbox every repository
// and every permission the installation has.
type TokenOptions struct {
	// Repositories limits the token to these repository names.
	Repositories []string
	// Permissions limits the token to these permissions, for example
	// {"contents": "write"}.
	Permissions map[string]string
}

// ScopedToken mints a token that is limited to the given repositories and
// permissions. The sandbox receives one of these, never the controller's own.
func (a *App) ScopedToken(ctx context.Context, opts TokenOptions) (string, error) {
	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	token, _, err := a.mint(ctx, jwt, opts)
	return token, err
}

// mint asks GitHub for an installation token.
func (a *App) mint(ctx context.Context, jwt string, opts ...TokenOptions) (string, time.Time, error) {
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", a.baseURL(), a.InstallationID)
	var body io.Reader
	if len(opts) > 0 {
		payload := map[string]any{}
		if len(opts[0].Repositories) > 0 {
			payload["repositories"] = opts[0].Repositories
		}
		if len(opts[0].Permissions) > 0 {
			payload["permissions"] = opts[0].Permissions
		}
		if len(payload) > 0 {
			encoded, err := json.Marshal(payload)
			if err != nil {
				return "", time.Time{}, fmt.Errorf("githubapp: token request: %w", err)
			}
			body = bytes.NewReader(encoded)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: token request: %w", err)
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: token response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", time.Time{}, fmt.Errorf("githubapp: token request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(response)))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(response, &out); err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: token response: %w", err)
	}
	if out.Token == "" {
		return "", time.Time{}, errors.New("githubapp: GitHub returned an empty token")
	}
	if out.ExpiresAt.IsZero() {
		// Fall back to the documented one hour when GitHub omits the field.
		out.ExpiresAt = a.now().Add(time.Hour)
	}
	return out.Token, out.ExpiresAt, nil
}

// Client returns a go-github client authenticated as the installation.
func (a *App) Client(ctx context.Context) (*github.Client, error) {
	httpClient, err := a.HTTPClientFor(ctx)
	if err != nil {
		return nil, err
	}
	opts := []github.ClientOptionsFunc{github.WithHTTPClient(httpClient)}
	if a.BaseURL != "" {
		base, err := url.Parse(a.baseURL() + "/")
		if err != nil {
			return nil, fmt.Errorf("githubapp: base url: %w", err)
		}
		// The upload endpoint is not used by the MVP, but leaving it at
		// api.github.com would make an Enterprise installation look valid
		// while every upload failed.
		opts = append(opts, github.WithEnterpriseURLs(base.String(), base.String()))
	}
	gh, err := github.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("githubapp: client: %w", err)
	}
	return gh, nil
}

// HTTPClientFor returns an HTTP client whose requests carry a fresh
// installation token. The token source is consulted per request, so a client
// that outlives the token still works.
func (a *App) HTTPClientFor(ctx context.Context) (*http.Client, error) {
	if _, err := a.Token(ctx); err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, &tokenSource{app: a}), nil
}

// tokenSource adapts App to oauth2.TokenSource.
type tokenSource struct{ app *App }

func (t *tokenSource) Token() (*oauth2.Token, error) {
	token, err := t.app.Token(context.Background())
	if err != nil {
		return nil, err
	}
	t.app.mu.Lock()
	expiry := t.app.expires
	t.app.mu.Unlock()
	return &oauth2.Token{AccessToken: token, Expiry: expiry}, nil
}

// jwt builds the RS256 app JWT GitHub expects.
func (a *App) jwt() (string, error) {
	key, err := parsePrivateKey(a.PrivateKey)
	if err != nil {
		return "", err
	}
	now := a.now()
	header := base64URL([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(tokenLifetime).Unix(),
		"iss": a.AppID,
	})
	if err != nil {
		return "", fmt.Errorf("githubapp: jwt claims: %w", err)
	}
	payload := header + "." + base64URL(claims)
	sum := sha256.Sum256([]byte(payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("githubapp: sign jwt: %w", err)
	}
	return payload + "." + base64URL(sig), nil
}

// parsePrivateKey accepts the PKCS#1 and PKCS#8 PEM forms GitHub hands out.
func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("githubapp: private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubapp: private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("githubapp: private key is not RSA")
	}
	return key, nil
}

func base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
