package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Helper process used for real cross-process locking tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	t.Setenv("PI_CODING_AGENT_DIR", os.Getenv("PI_CODING_AGENT_DIR_OVERRIDE"))
	CodexTokenURL = os.Getenv("MOCK_SERVER_URL")

	readyPath := os.Getenv("CHILD_READY_PATH")
	if readyPath != "" {
		testBeforeCodexAuthLock = func() {
			if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR writing ready file: %v\n", err)
				os.Exit(1)
			}
		}
		defer func() { testBeforeCodexAuthLock = nil }()
	}

	token, err := ResolveCodexToken(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	expectedToken := os.Getenv("EXPECTED_TOKEN")
	if expectedToken != "" && token != expectedToken {
		fmt.Fprintf(os.Stderr, "UNEXPECTED_TOKEN\n")
		os.Exit(1)
	}
	if token == "" {
		fmt.Fprintf(os.Stderr, "EMPTY_TOKEN\n")
		os.Exit(1)
	}

	fmt.Fprintln(os.Stdout, "SUCCESS")
	os.Exit(0)
}

func TestResolveCodexToken_StoredTokenRead(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	authPath := filepath.Join(tempDir, "auth.json")
	validCreds := map[string]*CodexOAuthCredential{
		"openai-codex": {
			Type:    "oauth",
			Access:  "valid-access-token",
			Refresh: "valid-refresh-token",
			Expires: time.Now().UnixMilli() + 3600000, // 1 hour in future
		},
	}
	data, err := json.Marshal(validCreds)
	if err != nil {
		t.Fatalf("failed to marshal credentials: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	token, err := ResolveCodexToken(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token != "valid-access-token" {
		t.Errorf("expected token 'valid-access-token', got %q", token)
	}
}

func TestResolveCodexToken_StoredTokenRead_EmptyAccessWithRefresh(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	// Write credential with empty access token, but valid refresh token
	authPath := filepath.Join(tempDir, "auth.json")
	validCreds := map[string]*CodexOAuthCredential{
		"openai-codex": {
			Type:    "oauth",
			Access:  "",
			Refresh: "valid-refresh-token",
			Expires: time.Now().UnixMilli() + 3600000,
		},
	}
	data, err := json.Marshal(validCreds)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	// Mock OAuth Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := tokenResponse{
			AccessToken:  "refreshed-access-token",
			RefreshToken: "refreshed-refresh-token",
			ExpiresIn:    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	oldTokenURL := CodexTokenURL
	CodexTokenURL = server.URL
	defer func() { CodexTokenURL = oldTokenURL }()

	// Resolve should notice access is empty, lock, refresh, and retrieve refreshed token
	token, err := ResolveCodexToken(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token != "refreshed-access-token" {
		t.Errorf("expected 'refreshed-access-token', got %q", token)
	}
}

func TestResolveCodexToken_ExpiredTokenRefresh(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	authPath := filepath.Join(tempDir, "auth.json")
	expiredCreds := map[string]*CodexOAuthCredential{
		"openai-codex": {
			Type:    "oauth",
			Access:  "old-access-token",
			Refresh: "old-refresh-token",
			Expires: time.Now().UnixMilli() - 1000, // expired
		},
	}
	data, err := json.Marshal(expiredCreds)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := tokenResponse{
			AccessToken:  "new-access-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	oldTokenURL := CodexTokenURL
	CodexTokenURL = server.URL
	defer func() { CodexTokenURL = oldTokenURL }()

	token, err := ResolveCodexToken(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token != "new-access-token" {
		t.Errorf("expected 'new-access-token', got %q", token)
	}
}

func TestResolveCodexToken_UnsupportedCredentialType(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	authPath := filepath.Join(tempDir, "auth.json")
	badCreds := map[string]*CodexOAuthCredential{
		"openai-codex": {
			Type:    "api_key",
			Access:  "some-key",
			Refresh: "",
			Expires: 0,
		},
	}
	data, err := json.Marshal(badCreds)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	_, err = ResolveCodexToken(context.Background())
	if err == nil {
		t.Error("expected error for unsupported credential type, got nil")
	}
}

func TestResolveCodexToken_NoEnvFallback(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-openai-key")
	t.Setenv("CODEX_ACCESS_TOKEN", "env-codex-token")

	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	_, err = ResolveCodexToken(context.Background())
	if err == nil {
		t.Fatal("expected error since auth.json does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' error, got: %v", err)
	}
}

func TestSaveCodexCredential_Permissions(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-perms-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	nestedDir := filepath.Join(tempDir, "nested", "path")
	filePath := filepath.Join(nestedDir, "auth.json")

	cred := &CodexOAuthCredential{
		Type:    "oauth",
		Access:  "test-access",
		Refresh: "test-refresh",
		Expires: time.Now().UnixMilli() + 3600000,
	}

	if err := saveCodexCredential(filePath, cred); err != nil {
		t.Fatalf("saveCodexCredential failed: %v", err)
	}

	// Verify parent directory perms are 0700
	dirInfo, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("failed to stat nestedDir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("expected nestedDir permission 0700, got %o", dirInfo.Mode().Perm())
	}

	parentDir := filepath.Dir(nestedDir)
	parentInfo, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("failed to stat parentDir: %v", err)
	}
	if parentInfo.Mode().Perm() != 0o700 {
		t.Errorf("expected parentDir permission 0700, got %o", parentInfo.Mode().Perm())
	}

	// Verify file permission is 0600
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat filePath: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Errorf("expected file permission 0600, got %o", fileInfo.Mode().Perm())
	}
}

func TestSaveCodexCredential_TightenPermissions(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-tighten-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	nestedDir := filepath.Join(tempDir, "nested")
	// 1. Create directory with loose 0755 permissions
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("failed to create nestedDir: %v", err)
	}
	if err := os.Chmod(nestedDir, 0o755); err != nil {
		t.Fatalf("failed to set nestedDir to 0755: %v", err)
	}

	filePath := filepath.Join(nestedDir, "auth.json")
	lockPath := filepath.Join(nestedDir, "auth.json.lock")

	// 2. Write files with loose 0644 permissions
	if err := os.WriteFile(filePath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}
	if err := os.Chmod(filePath, 0o644); err != nil {
		t.Fatalf("failed to chmod auth file to 0644: %v", err)
	}

	if err := os.WriteFile(lockPath, []byte(""), 0o644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}
	if err := os.Chmod(lockPath, 0o644); err != nil {
		t.Fatalf("failed to chmod lock file to 0644: %v", err)
	}

	// Double check initial loose permissions
	dirInfo, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("failed to stat nestedDir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("initial dir perm not 0755, got %o", dirInfo.Mode().Perm())
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat filePath: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o644 {
		t.Fatalf("initial file perm not 0644, got %o", fileInfo.Mode().Perm())
	}

	lockInfo, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("failed to stat lockPath: %v", err)
	}
	if lockInfo.Mode().Perm() != 0o644 {
		t.Fatalf("initial lock perm not 0644, got %o", lockInfo.Mode().Perm())
	}

	cred := &CodexOAuthCredential{
		Type:    "oauth",
		Access:  "test-access",
		Refresh: "test-refresh",
		Expires: time.Now().UnixMilli() + 3600000,
	}

	// Enforce via saveCodexCredential
	if err := saveCodexCredential(filePath, cred); err != nil {
		t.Fatalf("saveCodexCredential failed: %v", err)
	}

	// Verify directory perm is tightened to 0700
	dirInfo, err = os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("failed to stat nestedDir after save: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("expected tightened dir permission 0700, got %o", dirInfo.Mode().Perm())
	}

	// Verify file perm is tightened to 0600
	fileInfo, err = os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat filePath after save: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Errorf("expected tightened file permission 0600, got %o", fileInfo.Mode().Perm())
	}

	// Trigger ResolveCodexToken to lock & refresh, which will tighten lock file permission
	t.Setenv("PI_CODING_AGENT_DIR", nestedDir)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := tokenResponse{
			AccessToken:  "ref-token",
			RefreshToken: "ref-refresh",
			ExpiresIn:    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	oldTokenURL := CodexTokenURL
	CodexTokenURL = server.URL
	defer func() { CodexTokenURL = oldTokenURL }()

	// Overwrite auth.json with expired token to force lock & refresh
	expiredCreds := map[string]*CodexOAuthCredential{
		"openai-codex": {
			Type:    "oauth",
			Access:  "old",
			Refresh: "old-refresh",
			Expires: time.Now().UnixMilli() - 1000,
		},
	}
	expData, err := json.Marshal(expiredCreds)
	if err != nil {
		t.Fatalf("failed to marshal expired creds: %v", err)
	}
	if err := os.WriteFile(filePath, expData, 0o644); err != nil {
		t.Fatalf("failed to write expired auth file: %v", err)
	}
	if err := os.Chmod(filePath, 0o644); err != nil {
		t.Fatalf("failed to set expired auth file to 0644: %v", err)
	}

	_, err = ResolveCodexToken(context.Background())
	if err != nil {
		t.Fatalf("ResolveCodexToken failed: %v", err)
	}

	// Verify lock file perm is tightened to 0600
	lockInfo, err = os.Stat(lockPath)
	if err != nil {
		t.Fatalf("failed to stat lockPath after resolve: %v", err)
	}
	if lockInfo.Mode().Perm() != 0o600 {
		t.Errorf("expected tightened lock file permission 0600, got %o", lockInfo.Mode().Perm())
	}
}

func TestSaveCodexCredential_CorruptJSON(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-corrupt-save-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "auth.json")
	corruptData := "{invalid-json-data"
	if err := os.WriteFile(filePath, []byte(corruptData), 0o600); err != nil {
		t.Fatalf("failed to write corrupt auth: %v", err)
	}

	cred := &CodexOAuthCredential{
		Type:    "oauth",
		Access:  "test-access",
		Refresh: "test-refresh",
		Expires: time.Now().UnixMilli() + 3600000,
	}

	err = saveCodexCredential(filePath, cred)
	if err == nil {
		t.Fatal("expected error from saveCodexCredential with corrupt file JSON, got nil")
	}

	// Verify file content remains unchanged
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read auth: %v", err)
	}
	if string(content) != corruptData {
		t.Errorf("expected auth.json content to stay %q, got %q", corruptData, string(content))
	}
}

func TestResolveCodexToken_CrossProcessLock(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	authPath := filepath.Join(tempDir, "auth.json")
	expiredCreds := map[string]*CodexOAuthCredential{
		"openai-codex": {
			Type:    "oauth",
			Access:  "old-access-token",
			Refresh: "old-refresh-token",
			Expires: time.Now().UnixMilli() - 1000,
		},
	}
	data, err := json.Marshal(expiredCreds)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	var serverCallCount int32
	releaseRefresh := make(chan struct{})
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&serverCallCount, 1)
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-releaseRefresh

		w.Header().Set("Content-Type", "application/json")
		resp := tokenResponse{
			AccessToken:  "subprocess-access-token",
			RefreshToken: "subprocess-refresh-token",
			ExpiresIn:    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	// Acquire flock in the parent process first to coordinate starting state deterministically.
	lockPath := filepath.Join(tempDir, "auth.json.lock")
	parentLockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("parent failed to open lock file: %v", err)
	}
	if err := syscall.Flock(int(parentLockFile.Fd()), syscall.LOCK_EX); err != nil {
		parentLockFile.Close()
		t.Fatalf("parent failed to acquire lock: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	var out1, out2 []byte
	var err1, err2 error

	runCmd := func(readyPath string, out *[]byte, cmdErr *error) {
		defer wg.Done()
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(
			os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"PI_CODING_AGENT_DIR_OVERRIDE="+tempDir,
			"MOCK_SERVER_URL="+server.URL,
			"CHILD_READY_PATH="+readyPath,
			"EXPECTED_TOKEN=subprocess-access-token",
		)
		*out, *cmdErr = cmd.CombinedOutput()
	}

	child1Ready := filepath.Join(tempDir, "child1.ready")
	child2Ready := filepath.Join(tempDir, "child2.ready")
	go runCmd(child1Ready, &out1, &err1)
	go runCmd(child2Ready, &out2, &err2)

	// Wait until both children have reached the flock attempt inside ResolveCodexToken.
	start := time.Now()
	for {
		_, child1Err := os.Stat(child1Ready)
		_, child2Err := os.Stat(child2Ready)
		if child1Err == nil && child2Err == nil {
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("timeout waiting for child processes to reach flock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := syscall.Flock(int(parentLockFile.Fd()), syscall.LOCK_UN); err != nil {
		t.Errorf("parent failed to release flock: %v", err)
	}
	if err := parentLockFile.Close(); err != nil {
		t.Errorf("parent failed to close lock file: %v", err)
	}

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for refresh request to start")
	}
	close(releaseRefresh)

	wg.Wait()

	if err1 != nil {
		t.Errorf("Subprocess 1 failed: %v. Output:\n%s", err1, string(out1))
	}
	if err2 != nil {
		t.Errorf("Subprocess 2 failed: %v. Output:\n%s", err2, string(out2))
	}

	calls := atomic.LoadInt32(&serverCallCount)
	if calls != 1 {
		t.Errorf("expected exactly 1 server call (due to locking and re-reading), got %d", calls)
	}

	if !strings.Contains(string(out1), "SUCCESS") {
		t.Errorf("subprocess 1 output did not contain SUCCESS. Output:\n%s", string(out1))
	}
	if !strings.Contains(string(out2), "SUCCESS") {
		t.Errorf("subprocess 2 output did not contain SUCCESS. Output:\n%s", string(out2))
	}
}

func TestResolveCodexToken_ScrubRefreshErrorBody(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	authPath := filepath.Join(tempDir, "auth.json")
	expiredCreds := map[string]*CodexOAuthCredential{
		"openai-codex": {
			Type:    "oauth",
			Access:  "old-access-token",
			Refresh: "old-refresh-token",
			Expires: time.Now().UnixMilli() - 1000,
		},
	}
	data, err := json.Marshal(expiredCreds)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("failed to write auth: %v", err)
	}

	// Mock server returns 500 Internal Server Error with a highly sensitive token/secret in the body
	secretPayload := "SENSITIVE_OAUTH_TOKEN_AND_CLIENT_SECRET_PAYLOAD_DO_NOT_LEAK"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte(secretPayload)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	oldTokenURL := CodexTokenURL
	CodexTokenURL = server.URL
	defer func() { CodexTokenURL = oldTokenURL }()

	_, err = ResolveCodexToken(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if strings.Contains(err.Error(), secretPayload) {
		t.Errorf("error leaked sensitive payload! Error message: %v", err)
	}
}

func TestResolveCodexToken_InvalidExpiresIn(t *testing.T) {
	tests := []struct {
		name      string
		expiresIn int64
	}{
		{"zero_expires", 0},
		{"negative_expires", -10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
			if err != nil {
				t.Fatalf("failed to create temp: %v", err)
			}
			defer os.RemoveAll(tempDir)
			t.Setenv("PI_CODING_AGENT_DIR", tempDir)

			authPath := filepath.Join(tempDir, "auth.json")
			expiredCreds := map[string]*CodexOAuthCredential{
				"openai-codex": {
					Type:    "oauth",
					Access:  "old-access-token",
					Refresh: "old-refresh-token",
					Expires: time.Now().UnixMilli() - 1000,
				},
			}
			data, err := json.Marshal(expiredCreds)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}
			if err := os.WriteFile(authPath, data, 0o600); err != nil {
				t.Fatalf("failed to write auth: %v", err)
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				resp := tokenResponse{
					AccessToken:  "new-access-token",
					RefreshToken: "new-refresh-token",
					ExpiresIn:    tt.expiresIn,
				}
				if err := json.NewEncoder(w).Encode(resp); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			}))
			defer server.Close()

			oldTokenURL := CodexTokenURL
			CodexTokenURL = server.URL
			defer func() { CodexTokenURL = oldTokenURL }()

			_, err = ResolveCodexToken(context.Background())
			if err == nil {
				t.Fatal("expected error for invalid expires_in, got nil")
			}
			if !strings.Contains(err.Error(), "expires_in") {
				t.Fatalf("expected expires_in error, got: %v", err)
			}
		})
	}
}

func TestResolveCodexToken_CorruptAuthJSON(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	authPath := filepath.Join(tempDir, "auth.json")
	corruptData := "{invalid-json-data"
	if err := os.WriteFile(authPath, []byte(corruptData), 0o600); err != nil {
		t.Fatalf("failed to write corrupt auth: %v", err)
	}

	_, err = ResolveCodexToken(context.Background())
	if err == nil {
		t.Fatal("expected error parsing corrupt JSON, got nil")
	}

	// Verify file content has NOT been overwritten/truncated
	content, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("failed to read auth path: %v", err)
	}
	if string(content) != corruptData {
		t.Errorf("corrupt auth.json was overwritten! Content: %s", string(content))
	}
}

func TestResolveCodexToken_CancellableLock(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	authPath := filepath.Join(tempDir, "auth.json")
	expiredCreds := map[string]*CodexOAuthCredential{
		"openai-codex": {
			Type:    "oauth",
			Access:  "old-access",
			Refresh: "old-refresh",
			Expires: time.Now().UnixMilli() - 1000,
		},
	}
	data, _ := json.Marshal(expiredCreds)
	_ = os.WriteFile(authPath, data, 0o600)

	lockPath := filepath.Join(tempDir, "auth.json.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("failed to open lock file: %v", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("failed to acquire parent flock: %v", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = ResolveCodexToken(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context deadline exceeded or canceled, got: %v", err)
	}
}
