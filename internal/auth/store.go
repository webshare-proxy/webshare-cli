// Package auth signs the CLI in to Webshare over OAuth and keeps the tokens.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
)

const keyringService = "webshare-cli"

// ErrNoCredentials reports that nobody has logged in against this API host.
var ErrNoCredentials = errors.New("not logged in")

// Credentials is what a completed login leaves behind. The endpoints are kept
// alongside the tokens so an ordinary command never has to fetch the server's
// metadata again just to refresh.
type Credentials struct {
	AccessToken        string    `json:"access_token"`
	RefreshToken       string    `json:"refresh_token"`
	Expiry             time.Time `json:"expiry"`
	Scopes             []string  `json:"scopes"`
	Issuer             string    `json:"issuer"`
	TokenEndpoint      string    `json:"token_endpoint"`
	RevocationEndpoint string    `json:"revocation_endpoint"`
}

// accountFor keys the stored credentials by API host, so a login against a
// test environment never stands in for the production one.
func accountFor(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return baseURL
	}
	return parsed.Host
}

// configDir is where the CLI keeps what it has to keep on disk. It never
// guesses: a relative path would drop a live token into whatever directory the
// command was run from.
func configDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding somewhere to keep the login: %w", err)
	}
	return filepath.Join(dir, "webshare"), nil
}

// fallbackPath is where the tokens go when the operating system has no
// keyring to put them in.
func fallbackPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// lockRefresh waits until no other webshare command is refreshing the login
// and returns the function that lets the next one in. The operating system
// releases the lock when its holder exits, so a command that crashes midway
// leaves nobody waiting on it.
func lockRefresh(ctx context.Context) (unlock func(), err error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	lock := flock.New(filepath.Join(dir, "refresh.lock"))
	if _, err := lock.TryLockContext(ctx, 50*time.Millisecond); err != nil {
		return nil, fmt.Errorf("waiting for another webshare command to refresh the login: %w", err)
	}
	return func() { _ = lock.Unlock() }, nil
}

// Save stores the credentials in the OS keyring, falling back to a file only
// the owner can read when there is no keyring to reach.
func Save(baseURL string, credentials *Credentials) error {
	body, err := json.Marshal(credentials)
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}
	account := accountFor(baseURL)
	if keyring.Set(keyringService, account, string(body)) == nil {
		// An earlier fallback would go stale here: Load prefers the keyring,
		// so that copy would sit on disk and never be read again.
		return removeFromFile(account)
	}
	return saveToFile(account, body)
}

// Load returns the credentials stored for the API host, or ErrNoCredentials.
func Load(baseURL string) (*Credentials, error) {
	account := accountFor(baseURL)
	body, err := keyring.Get(keyringService, account)
	if err != nil {
		stored, fileErr := readFile()
		if fileErr != nil {
			return nil, fileErr
		}
		raw, ok := stored[account]
		if !ok {
			// A keyring that is there but will not answer is not the same as
			// nobody having logged in, and logging in again would not fix it.
			if !errors.Is(err, keyring.ErrNotFound) && !errors.Is(err, keyring.ErrUnsupportedPlatform) {
				return nil, fmt.Errorf("reading the login from the keyring: %w", err)
			}
			return nil, ErrNoCredentials
		}
		body = string(raw)
	}
	credentials := &Credentials{}
	if err := json.Unmarshal([]byte(body), credentials); err != nil {
		return nil, fmt.Errorf("reading the stored login: %w", err)
	}
	return credentials, nil
}

// Clear forgets the credentials for the API host. Clearing something that was
// never there is not an error.
func Clear(baseURL string) error {
	account := accountFor(baseURL)
	_ = keyring.Delete(keyringService, account)
	return removeFromFile(account)
}

func removeFromFile(account string) error {
	stored, err := readFile()
	if err != nil {
		return err
	}
	if _, ok := stored[account]; !ok {
		return nil
	}
	delete(stored, account)
	return writeFile(stored)
}

// readFile tolerates having nowhere to read from: with no config directory
// there is no fallback file, which is not an error until something needs to
// be written.
func readFile() (map[string]json.RawMessage, error) {
	path, err := fallbackPath()
	if err != nil {
		return map[string]json.RawMessage{}, nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	stored := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &stored); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return stored, nil
}

func saveToFile(account string, body []byte) error {
	stored, err := readFile()
	if err != nil {
		return err
	}
	stored[account] = json.RawMessage(body)
	return writeFile(stored)
}

func writeFile(stored map[string]json.RawMessage) error {
	body, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}

	path, err := fallbackPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	// 0600: with no keyring this file is the only thing guarding a live token.
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
