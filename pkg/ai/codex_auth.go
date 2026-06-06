package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Upstream OAuth constants needed for refresh/login compatibility.
const (
	CodexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	CodexAuthBaseURL  = "https://auth.openai.com"
	CodexAuthorizeURL = CodexAuthBaseURL + "/oauth/authorize"
	CodexRedirectURI  = "http://localhost:1455/auth/callback"
)

var CodexTokenURL = CodexAuthBaseURL + "/oauth/token"

// CodexOAuthCredential represents the structure of the "openai-codex" credential
// inside auth.json.
type CodexOAuthCredential struct {
	Type    string `json:"type"`
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Expires int64  `json:"expires"` // Unix timestamp in milliseconds
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // in seconds
}

var (
	authMutex               sync.Mutex
	testBeforeCodexAuthLock func()
)

// ResolveCodexToken resolves the Codex OAuth access token.
// If the token is expired, it locks the companion auth.json.lock, re-checks
// the file, performs the refresh request if still expired, and writes the
// updated credentials atomically to auth.json.
func ResolveCodexToken(ctx context.Context) (string, error) {
	filePath, lockPath, err := getAuthFilePaths()
	if err != nil {
		return "", err
	}

	// 1. Thread-safe fast path: check if it's already valid without cross-process locking
	authMutex.Lock()
	cred, err := loadCodexCredential(filePath)
	if err == nil && cred.Access != "" && time.Now().UnixMilli() < cred.Expires {
		accessToken := cred.Access
		authMutex.Unlock()
		return accessToken, nil
	}
	authMutex.Unlock()

	// 2. Slow path: acquire cross-process lock (requires both package mutex and flock)
	authMutex.Lock()
	defer authMutex.Unlock()

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create credentials directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to enforce credentials directory permissions: %w", err)
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("failed to open lock file: %w", err)
	}
	defer lockFile.Close()

	if err := lockFile.Chmod(0o600); err != nil {
		return "", fmt.Errorf("failed to enforce lock file permissions: %w", err)
	}

	if testBeforeCodexAuthLock != nil {
		testBeforeCodexAuthLock()
	}
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()

	for {
		flockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if flockErr == nil {
			break
		}
		if flockErr != syscall.EWOULDBLOCK && flockErr != syscall.EAGAIN {
			return "", fmt.Errorf("failed to acquire flock: %w", flockErr)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			timer.Reset(10 * time.Millisecond)
		}
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// 3. Re-read under lock to check if another process refreshed it
	cred, err = loadCodexCredential(filePath)
	if err == nil && cred.Access != "" && time.Now().UnixMilli() < cred.Expires {
		return cred.Access, nil
	}
	if err != nil {
		return "", err
	}

	// 4. Refresh token
	if cred.Refresh == "" {
		return "", errors.New("refresh token is empty, cannot refresh credentials")
	}

	newCred, err := refreshCodexToken(ctx, cred.Refresh)
	if err != nil {
		return "", fmt.Errorf("failed to refresh oauth token: %w", err)
	}

	// 5. Update auth.json
	if err := saveCodexCredential(filePath, newCred); err != nil {
		return "", fmt.Errorf("failed to save refreshed credentials: %w", err)
	}

	return newCred.Access, nil
}

func getAuthFilePaths() (string, string, error) {
	dir := os.Getenv("PI_CODING_AGENT_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("unable to resolve user home directory: %w", err)
		}
		dir = filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(dir, "auth.json"), filepath.Join(dir, "auth.json.lock"), nil
}

func loadCodexCredential(filePath string) (*CodexOAuthCredential, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("credentials file %q does not exist", filePath)
		}
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}

	var store map[string]json.RawMessage
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("failed to parse credentials JSON: %w", err)
	}

	raw, ok := store["openai-codex"]
	if !ok {
		return nil, errors.New("credentials file missing 'openai-codex' entry")
	}

	var cred CodexOAuthCredential
	if err := json.Unmarshal(raw, &cred); err != nil {
		return nil, fmt.Errorf("failed to parse 'openai-codex' credentials: %w", err)
	}

	if cred.Type != "oauth" {
		return nil, fmt.Errorf("unsupported credential type %q (expected %q)", cred.Type, "oauth")
	}

	isExpired := time.Now().UnixMilli() >= cred.Expires
	if (cred.Access == "" || isExpired) && cred.Refresh == "" {
		return nil, errors.New("credentials token is expired/empty and refresh token is missing")
	}

	return &cred, nil
}

func saveCodexCredential(filePath string, newCred *CodexOAuthCredential) error {
	var store map[string]json.RawMessage
	data, err := os.ReadFile(filePath)
	if err == nil {
		if err := json.Unmarshal(data, &store); err != nil {
			return fmt.Errorf("failed to parse existing credentials before saving: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read credentials file before saving: %w", err)
	}
	if store == nil {
		store = make(map[string]json.RawMessage)
	}

	newRaw, err := json.Marshal(newCred)
	if err != nil {
		return fmt.Errorf("failed to marshal new credential: %w", err)
	}
	store["openai-codex"] = newRaw

	jsonData, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal store to JSON: %w", err)
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("failed to enforce directory permissions: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "auth.json.tmp.*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpName)
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}

	if _, err := tmpFile.Write(jsonData); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, filePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func refreshCodexToken(ctx context.Context, refreshToken string) (*CodexOAuthCredential, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", CodexClientID)

	req, err := http.NewRequestWithContext(ctx, "POST", CodexTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth token refresh network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth token refresh failed with status %d", resp.StatusCode)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("failed to decode refresh response: %w", err)
	}

	if tr.AccessToken == "" {
		return nil, errors.New("refresh response missing access_token")
	}
	if tr.ExpiresIn <= 0 {
		return nil, errors.New("refresh response missing valid expires_in")
	}

	newRefreshToken := tr.RefreshToken
	if newRefreshToken == "" {
		newRefreshToken = refreshToken
	}

	return &CodexOAuthCredential{
		Type:    "oauth",
		Access:  tr.AccessToken,
		Refresh: newRefreshToken,
		Expires: time.Now().UnixMilli() + tr.ExpiresIn*1000,
	}, nil
}
