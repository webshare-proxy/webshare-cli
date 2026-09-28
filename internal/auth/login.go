package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	webshare "github.com/webshare-proxy/webshare-go"
	"golang.org/x/oauth2"
)

// DefaultScopes is what login asks for: everything the CLI's own commands
// need, and nothing beyond them.
var DefaultScopes = []string{
	"account:read",
	"billing:read",
	"proxy:read",
	"subuser:read",
	"proxy:write",
	"subuser:write",
	"account:write",
}

// Config describes a login: which server, as which client, asking for what.
type Config struct {
	BaseURL  string
	ClientID string
	Scopes   []string
	// HTTPClient carries the caller's transport, which is how --insecure
	// reaches a test environment's self-signed certificate.
	HTTPClient *http.Client
	// OpenBrowser hands the authorization URL to the person's browser. Nil
	// uses whatever the operating system opens URLs with.
	OpenBrowser func(url string) error
	// Output is where the URL is printed, so the login still completes when
	// the browser cannot be opened.
	Output io.Writer
	// Timeout bounds the wait for the person to approve in the browser. Zero
	// means DefaultTimeout.
	Timeout time.Duration
}

// DefaultTimeout is how long a login waits for the browser. The server drops
// a pending authorization request after ten minutes, so waiting longer than
// that only hides a login that can no longer succeed.
const DefaultTimeout = 10 * time.Minute

// requestTimeout bounds a single call to the authorization server.
const requestTimeout = 30 * time.Second

func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	// Not http.DefaultClient: it has no timeout, so a server that accepts the
	// connection and then says nothing would hang the command for good.
	return &http.Client{Timeout: requestTimeout}
}

// oauthContext hands the caller's transport to golang.org/x/oauth2, which
// reads it from the context rather than from the config.
func (c Config) oauthContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, c.client())
}

func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

func (c Config) scopes() []string {
	if len(c.Scopes) > 0 {
		return c.Scopes
	}
	return DefaultScopes
}

// resource is the API the token is for (RFC 8707). The authorization server
// requires one and only issues tokens for the servers it knows.
func (c Config) resource() string {
	return strings.TrimSuffix(c.BaseURL, "/") + "/api"
}

func (c Config) open(rawURL string) error {
	if c.Output != nil {
		fmt.Fprintf(c.Output, "Opening your browser to sign in. If nothing happens, open this:\n%s\n", rawURL)
	}
	if c.OpenBrowser != nil {
		return c.OpenBrowser(rawURL)
	}
	return openInBrowser(rawURL)
}

// metadata is the part of RFC 8414 discovery the CLI uses.
type metadata struct {
	Issuer                 string `json:"issuer"`
	AuthorizationEndpoint  string `json:"authorization_endpoint"`
	TokenEndpoint          string `json:"token_endpoint"`
	RevocationEndpoint     string `json:"revocation_endpoint"`
	IssuerInResponseIsSent bool   `json:"authorization_response_iss_parameter_supported"`
}

// discover reads the server's own description of itself, so the CLI carries no
// hard-coded paths and points at a test environment by base URL alone.
func discover(ctx context.Context, cfg Config) (*metadata, error) {
	endpoint := strings.TrimSuffix(cfg.BaseURL, "/") + "/.well-known/oauth-authorization-server"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("asking %s who it is: %w", cfg.BaseURL, err)
	}
	response, err := cfg.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("asking %s who it is: %w", cfg.BaseURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s does not describe an authorization server (HTTP %d)", endpoint, response.StatusCode)
	}
	server := &metadata{}
	if err := json.NewDecoder(response.Body).Decode(server); err != nil {
		return nil, fmt.Errorf("reading %s: %w", endpoint, err)
	}
	if server.Issuer == "" || server.AuthorizationEndpoint == "" || server.TokenEndpoint == "" {
		return nil, fmt.Errorf("%s is missing the endpoints a login needs", endpoint)
	}
	return server, nil
}

// Login runs the authorization code flow with PKCE against a loopback
// redirect (RFC 8252) and stores what it gets back.
func Login(ctx context.Context, cfg Config) (*Credentials, error) {
	server, err := discover(ctx, cfg)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("opening a port for the browser to come back to: %w", err)
	}
	defer listener.Close()

	verifier := oauth2.GenerateVerifier()
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	flow := &oauth2.Config{
		ClientID:    cfg.ClientID,
		RedirectURL: fmt.Sprintf("http://%s/callback", listener.Addr().String()),
		Scopes:      cfg.scopes(),
		Endpoint: oauth2.Endpoint{
			AuthURL:  server.AuthorizationEndpoint,
			TokenURL: server.TokenEndpoint,
			// A public client sends no secret, so there is nothing to put in
			// an Authorization header.
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}

	callbacks := make(chan callback, 1)
	browserServer := &http.Server{
		Handler:           callbackHandler(state, server, callbacks),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = browserServer.Serve(listener) }()
	defer func() {
		stopping, stopped := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopped()
		_ = browserServer.Shutdown(stopping)
	}()

	if err := cfg.open(flow.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("resource", cfg.resource()),
	)); err != nil {
		return nil, fmt.Errorf("opening the browser: %w", err)
	}

	waiting, stopWaiting := context.WithTimeout(ctx, cfg.timeout())
	defer stopWaiting()
	var answer callback
	select {
	case <-waiting.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("gave up after %s waiting for the browser to come back", cfg.timeout())
	case answer = <-callbacks:
	}
	if answer.err != nil {
		return nil, answer.err
	}
	token, err := flow.Exchange(cfg.oauthContext(ctx), answer.code,
		oauth2.VerifierOption(verifier),
		oauth2.SetAuthURLParam("resource", cfg.resource()),
	)
	if err != nil {
		return nil, fmt.Errorf("exchanging the authorization code: %w", err)
	}
	credentials := &Credentials{
		AccessToken:        token.AccessToken,
		RefreshToken:       token.RefreshToken,
		Expiry:             token.Expiry,
		Scopes:             grantedScopes(token, cfg.scopes()),
		Issuer:             server.Issuer,
		TokenEndpoint:      server.TokenEndpoint,
		RevocationEndpoint: server.RevocationEndpoint,
	}
	if err := Save(cfg.BaseURL, credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// grantedScopes is what the account approved, which can be less than the CLI
// asked for: the consent screen lets a person untick what they would rather
// not hand over (RFC 6749, 3.3).
func grantedScopes(token *oauth2.Token, requested []string) []string {
	granted, _ := token.Extra("scope").(string)
	if strings.TrimSpace(granted) == "" {
		return requested
	}
	return strings.Fields(granted)
}

// callback is what came back on the loopback port.
type callback struct {
	code string
	err  error
}

// callbackHandler answers the one request the browser makes. Any process on
// the machine can reach this port, so a request only counts as ours when it
// carries back the state this login generated.
func callbackHandler(state string, server *metadata, callbacks chan<- callback) http.Handler {
	refuse := func(w http.ResponseWriter, status int, page string, err error) {
		http.Error(w, page, status)
		report(callbacks, callback{err: err})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		if query.Get("state") != state {
			refuse(w, http.StatusBadRequest, "This did not come from the sign-in you started.",
				errors.New("the browser came back with a different state, so this was not the sign-in this command started"))
			return
		}
		issuer := query.Get("iss")
		if (issuer == "" && server.IssuerInResponseIsSent) || (issuer != "" && issuer != server.Issuer) {
			refuse(w, http.StatusBadRequest, "This did not come from Webshare.",
				fmt.Errorf("the browser came back from %q rather than %s", issuer, server.Issuer))
			return
		}
		if failure := query.Get("error"); failure != "" {
			description := query.Get("error_description")
			if description == "" {
				description = failure
			}
			refuse(w, http.StatusForbidden, "Sign-in refused: "+description, errors.New(description))
			return
		}
		code := query.Get("code")
		if code == "" {
			refuse(w, http.StatusBadRequest, "The sign-in did not come back with a code.",
				errors.New("the browser came back without an authorization code"))
			return
		}
		_, _ = io.WriteString(w, "Signed in to Webshare. You can close this tab.")
		report(callbacks, callback{code: code})
	})
}

// report hands the outcome to Login without ever blocking. Login takes the
// first answer and may already have given up, and a handler parked on a send
// would keep the server from shutting down.
func report(callbacks chan<- callback, answer callback) {
	select {
	case callbacks <- answer:
	default:
	}
}

// Logout revokes the tokens and only then forgets them. Forgetting alone
// would leave a token the API still answers.
func Logout(ctx context.Context, cfg Config, credentials *Credentials) error {
	// The refresh token goes first: revoking it takes the whole family with it.
	var failed error
	for _, token := range []string{credentials.RefreshToken, credentials.AccessToken} {
		if token == "" {
			continue
		}
		if err := revoke(ctx, cfg, credentials.RevocationEndpoint, token); err != nil && failed == nil {
			failed = err
		}
	}
	// The local copy goes whatever happened above: a server nobody can reach
	// must not be what stops someone taking a token off their own machine.
	if err := Clear(cfg.BaseURL); err != nil {
		return err
	}
	if failed != nil {
		return fmt.Errorf("%w; the token is off this machine but may still work, so revoke it under Connected Apps in the dashboard", failed)
	}
	return nil
}

func revoke(ctx context.Context, cfg Config, endpoint, token string) error {
	if endpoint == "" {
		return errors.New("the stored login does not say where to revoke tokens; log in again")
	}
	body := url.Values{"token": {token}, "client_id": {cfg.ClientID}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("revoking the token: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := cfg.client().Do(request)
	if err != nil {
		return fmt.Errorf("revoking the token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("revoking the token: the server answered HTTP %d", response.StatusCode)
	}
	return nil
}

// NewTokenSource presents the stored access token to the API and refreshes it
// when it expires. The context it is built with is the one a refresh uses,
// because golang.org/x/oauth2 takes no context per call.
func NewTokenSource(ctx context.Context, cfg Config, credentials *Credentials) webshare.TokenSource {
	flow := &oauth2.Config{
		ClientID: cfg.ClientID,
		Endpoint: oauth2.Endpoint{TokenURL: credentials.TokenEndpoint, AuthStyle: oauth2.AuthStyleInParams},
	}
	return &persistingSource{
		baseURL: cfg.BaseURL,
		stored:  credentials,
		source: flow.TokenSource(cfg.oauthContext(ctx), &oauth2.Token{
			AccessToken:  credentials.AccessToken,
			RefreshToken: credentials.RefreshToken,
			Expiry:       credentials.Expiry,
			TokenType:    "Bearer",
		}),
	}
}

// persistingSource writes the pair back whenever it rotates. The server
// rotates both tokens on every refresh and refuses a refresh token twice, so
// a rotation that is not stored leaves the next command with nothing usable.
type persistingSource struct {
	baseURL string
	// mutex guards stored: oauth2's own source is safe for concurrent use and
	// this wrapper has to be too, or two refreshes race and the loser spends a
	// refresh token the server will never accept again.
	mutex  sync.Mutex
	stored *Credentials
	source oauth2.TokenSource
}

func (p *persistingSource) Token(context.Context) (webshare.Token, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	token, err := p.source.Token()
	if err != nil {
		return webshare.Token{}, fmt.Errorf("refreshing the stored login (run `webshare login` again): %w", err)
	}
	if token.AccessToken != p.stored.AccessToken {
		p.stored.AccessToken = token.AccessToken
		p.stored.RefreshToken = token.RefreshToken
		p.stored.Expiry = token.Expiry
		if err := Save(p.baseURL, p.stored); err != nil {
			return webshare.Token{}, err
		}
	}
	return webshare.Token{Value: token.AccessToken, Scheme: "Bearer"}, nil
}

func randomState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating the sign-in state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func openInBrowser(rawURL string) error {
	command, args := "xdg-open", []string{rawURL}
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}
	}
	return exec.Command(command, args...).Start()
}
