package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// fakeAS stands in for the Webshare authorization server: it records what the
// CLI sent and answers with whatever the test needs it to answer.
type fakeAS struct {
	server *httptest.Server
	issuer string
	// callbackIssuer is what the callback claims to be; empty means the
	// truth, so a test can make the callback lie about where it came from.
	callbackIssuer string
	// state is echoed back on the callback; empty means echo what was sent.
	state string

	challenge     string
	authResource  string
	sentState     string
	verifier      string
	tokenResource string
	grantType     string
	revoked       []string
	refreshed     int

	// grantedScope is what the token response says was approved; empty means
	// the response carries no scope at all.
	grantedScope string
	// revokeStatus lets a test make revocation fail.
	revokeStatus int
}

func newFakeAS(t *testing.T) *fakeAS {
	t.Helper()
	as := &fakeAS{}
	as.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			writeJSON(w, map[string]any{
				"issuer":                 as.issuer,
				"authorization_endpoint": as.server.URL + "/oauth/authorize/",
				"token_endpoint":         as.server.URL + "/oauth/token/",
				"revocation_endpoint":    as.server.URL + "/oauth/revoke_token/",
			})
		case "/oauth/authorize/":
			query := r.URL.Query()
			as.challenge = query.Get("code_challenge")
			as.authResource = query.Get("resource")
			as.sentState = query.Get("state")
			if query.Get("code_challenge_method") != "S256" {
				t.Errorf("code_challenge_method = %q, want S256", query.Get("code_challenge_method"))
			}
			state := as.state
			if state == "" {
				state = as.sentState
			}
			callback, err := url.Parse(query.Get("redirect_uri"))
			if err != nil {
				t.Fatalf("parsing redirect_uri: %v", err)
			}
			issuer := as.callbackIssuer
			if issuer == "" {
				issuer = as.issuer
			}
			callback.RawQuery = url.Values{
				"code": {"the-code"}, "state": {state}, "iss": {issuer},
			}.Encode()
			http.Redirect(w, r, callback.String(), http.StatusFound)
		case "/oauth/token/":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parsing token form: %v", err)
			}
			as.grantType = r.PostForm.Get("grant_type")
			if as.grantType == "refresh_token" {
				as.refreshed++
				writeJSON(w, map[string]any{
					"access_token": "access-2", "refresh_token": "refresh-2",
					"token_type": "Bearer", "expires_in": 3600,
				})
				return
			}
			as.verifier = r.PostForm.Get("code_verifier")
			as.tokenResource = r.PostForm.Get("resource")
			body := map[string]any{
				"access_token": "access-1", "refresh_token": "refresh-1",
				"token_type": "Bearer", "expires_in": 3600,
			}
			if as.grantedScope != "" {
				body["scope"] = as.grantedScope
			}
			writeJSON(w, body)
		case "/oauth/revoke_token/":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parsing revoke form: %v", err)
			}
			as.revoked = append(as.revoked, r.PostForm.Get("token"))
			if as.revokeStatus != 0 {
				w.WriteHeader(as.revokeStatus)
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	as.issuer = as.server.URL
	t.Cleanup(as.server.Close)
	return as
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// config returns a Config whose browser fetches the authorize URL itself, the
// way a real one would, without blocking the login it is part of.
func (as *fakeAS) config(t *testing.T) Config {
	t.Helper()
	return Config{
		BaseURL:  as.server.URL,
		ClientID: "webshare-cli",
		Scopes:   DefaultScopes,
		Output:   io.Discard,
		OpenBrowser: func(rawURL string) error {
			go func() {
				response, err := http.Get(rawURL)
				if err == nil {
					_ = response.Body.Close()
				}
			}()
			return nil
		},
	}
}

// isolate points the keyring and the file fallback at this test alone.
func isolate(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestLoginProvesPossessionOfTheVerifierAndStoresTheTokens(t *testing.T) {
	isolate(t)
	as := newFakeAS(t)

	credentials, err := Login(context.Background(), as.config(t))
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	digest := sha256.Sum256([]byte(as.verifier))
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); as.challenge != want {
		t.Errorf("challenge = %q, want the S256 digest of the verifier %q", as.challenge, want)
	}
	if want := as.server.URL + "/api"; as.authResource != want || as.tokenResource != want {
		t.Errorf("resource = %q and %q, want %q on both", as.authResource, as.tokenResource, want)
	}
	if credentials.AccessToken != "access-1" || credentials.RefreshToken != "refresh-1" {
		t.Errorf("credentials = %+v", credentials)
	}
	stored, err := Load(as.server.URL)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.AccessToken != "access-1" {
		t.Errorf("stored access token = %q, want the one login returned", stored.AccessToken)
	}
}

func TestLoginRefusesACallbackThatChangesTheStateOrTheIssuer(t *testing.T) {
	for name, tamper := range map[string]func(*fakeAS){
		"state":  func(as *fakeAS) { as.state = "someone-elses-state" },
		"issuer": func(as *fakeAS) { as.callbackIssuer = "https://impostor.example.com" },
	} {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			as := newFakeAS(t)
			tamper(as)

			_, err := Login(context.Background(), as.config(t))

			if err == nil {
				t.Fatalf("Login accepted a callback with a tampered %s", name)
			}
			if _, loadErr := Load(as.server.URL); loadErr == nil {
				t.Error("a refused login still stored credentials")
			}
		})
	}
}

func TestExpiredAccessTokenIsRefreshedAndTheRotatedPairIsStored(t *testing.T) {
	isolate(t)
	as := newFakeAS(t)
	host := as.server.URL
	expired := &Credentials{
		AccessToken: "access-1", RefreshToken: "refresh-1",
		Expiry: time.Now().Add(-time.Hour), Issuer: as.issuer,
		TokenEndpoint: as.server.URL + "/oauth/token/",
	}
	if err := Save(host, expired); err != nil {
		t.Fatalf("Save: %v", err)
	}

	source := NewTokenSource(context.Background(), as.config(t), expired)
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if token.Value != "access-2" || token.Scheme != "Bearer" {
		t.Errorf("token = %+v, want the refreshed value as a Bearer", token)
	}
	stored, err := Load(host)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.AccessToken != "access-2" || stored.RefreshToken != "refresh-2" {
		t.Errorf("stored = %+v, want the rotated pair; PCP will not accept refresh-1 again", stored)
	}
}

func TestLogoutRevokesBothTokensBeforeClearingThem(t *testing.T) {
	isolate(t)
	as := newFakeAS(t)
	host := as.server.URL
	credentials := &Credentials{
		AccessToken: "access-1", RefreshToken: "refresh-1", Issuer: as.issuer,
		RevocationEndpoint: as.server.URL + "/oauth/revoke_token/",
	}
	if err := Save(host, credentials); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := Logout(context.Background(), as.config(t), credentials); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if strings.Join(as.revoked, ",") != "refresh-1,access-1" {
		t.Errorf("revoked = %v, want the refresh token and then the access token", as.revoked)
	}
	if _, err := Load(host); err == nil {
		t.Error("Logout left credentials behind")
	}
}

func TestCredentialsSurviveWithoutAKeyring(t *testing.T) {
	isolate(t)
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
	host := "https://proxy.webshare.io"

	if err := Save(host, &Credentials{AccessToken: "access-1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	stored, err := Load(host)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if stored.AccessToken != "access-1" {
		t.Errorf("stored access token = %q", stored.AccessToken)
	}
	path, err := fallbackPath()
	if err != nil {
		t.Fatalf("fallbackPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the file fallback was not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("fallback file mode = %o, want 600", mode)
	}
}

func TestLoginRecordsWhatWasGrantedRatherThanWhatWasAsked(t *testing.T) {
	isolate(t)
	as := newFakeAS(t)
	// The consent screen lets a person hand over less than the client asked
	// for (RFC 6749, 3.3), and the token response says what they settled on.
	as.grantedScope = "account:read proxy:read"

	credentials, err := Login(context.Background(), as.config(t))
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if strings.Join(credentials.Scopes, " ") != "account:read proxy:read" {
		t.Errorf("scopes = %v, want only the two that were granted", credentials.Scopes)
	}
}

func TestLoginGivesUpWaitingForABrowserThatNeverComesBack(t *testing.T) {
	isolate(t)
	as := newFakeAS(t)
	config := as.config(t)
	config.OpenBrowser = func(string) error { return nil }
	config.Timeout = 50 * time.Millisecond

	_, err := Login(context.Background(), config)

	if err == nil {
		t.Fatal("Login waited for a browser that never came back")
	}
}

func TestLogoutTakesTheTokenOffThisMachineEvenWhenRevocationFails(t *testing.T) {
	isolate(t)
	as := newFakeAS(t)
	as.revokeStatus = http.StatusInternalServerError
	host := as.server.URL
	credentials := &Credentials{
		AccessToken: "access-1", RefreshToken: "refresh-1", Issuer: as.issuer,
		RevocationEndpoint: as.server.URL + "/oauth/revoke_token/",
	}
	if err := Save(host, credentials); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := Logout(context.Background(), as.config(t), credentials)

	if err == nil {
		t.Error("Logout hid a failed revocation; the token may still work")
	}
	if _, loadErr := Load(host); loadErr == nil {
		t.Error("a failed revocation left the token on this machine, with no way to remove it")
	}
}

func TestAKeyringThatRefusesToAnswerIsNotReportedAsNobodyBeingLoggedIn(t *testing.T) {
	isolate(t)
	keyring.MockInitWithError(errors.New("the keyring is locked"))

	_, err := Load("https://proxy.webshare.io")

	if err == nil || errors.Is(err, ErrNoCredentials) {
		t.Errorf("err = %v, want the keyring's own failure; telling someone to log in again would not fix it", err)
	}
}

func TestCredentialsAreNotStoredWhenThereIsNowhereSafeToPutThem(t *testing.T) {
	isolate(t)
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
	// With no keyring and no config directory there is no safe place left. A
	// relative path would drop a live token into the working directory.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	err := Save("https://proxy.webshare.io", &Credentials{AccessToken: "access-1"})

	if err == nil {
		t.Fatal("Save invented a place to put the token")
	}
	if _, statErr := os.Stat(filepath.Join(".webshare", "credentials.json")); statErr == nil {
		t.Error("Save wrote a token into the working directory")
	}
}

func TestAKeyringWriteClearsAnEarlierFileFallback(t *testing.T) {
	isolate(t)
	host := "https://proxy.webshare.io"
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
	if err := Save(host, &Credentials{AccessToken: "from-the-file"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	keyring.MockInit()
	if err := Save(host, &Credentials{AccessToken: "from-the-keyring"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path, err := fallbackPath()
	if err != nil {
		t.Fatalf("fallbackPath: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the fallback file: %v", err)
	}
	if strings.Contains(string(body), "from-the-file") {
		t.Error("the file still holds a token nothing will ever read again")
	}
}
