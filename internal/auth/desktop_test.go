package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
	"golang.org/x/crypto/pbkdf2"
)

func TestDecryptCookie(t *testing.T) {
	password := "test-password"
	plaintext := "xoxd-test-cookie-value"

	encrypted := encryptForTest(t, password, []byte(plaintext))

	got, err := decryptCookie(encrypted, password)
	if err != nil {
		t.Fatal(err)
	}
	if got != plaintext {
		t.Errorf("got %q, want %q", got, plaintext)
	}
}

func TestDecryptCookie_WrongPassword(t *testing.T) {
	password := "correct-password"
	plaintext := "xoxd-test-cookie-value"

	encrypted := encryptForTest(t, password, []byte(plaintext))

	_, err := decryptCookie(encrypted, "wrong-password")
	if err == nil {
		t.Fatal("expected error with wrong password")
	}
}

// encryptForTest encrypts using Chrome/Electron's Safe Storage format:
// v10 prefix + AES-CBC(PBKDF2(password, "saltysalt", 1003, 16), iv=spaces).
func encryptForTest(t *testing.T, password string, plaintext []byte) []byte {
	t.Helper()
	key := pbkdf2.Key([]byte(password), []byte("saltysalt"), 1003, 16, sha1.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padLen := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padLen)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	iv := make([]byte, aes.BlockSize)
	for i := range iv {
		iv[i] = ' '
	}
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	return append([]byte("v10"), ct...)
}

func TestExtractTokensFromLevelDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "leveldb")

	config := map[string]any{
		"teams": map[string]any{
			"T01ABC": map[string]any{
				"token": "xoxc-workspace-token-1",
				"name":  "Test Workspace",
				"url":   "https://test.slack.com",
			},
			"T02DEF": map[string]any{
				"token": "xoxb-not-session",
				"name":  "Other",
			},
		},
	}
	configJSON, _ := json.Marshal(config)

	// Write a real LevelDB with the config.
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put([]byte("localConfig_v2"), configJSON, nil); err != nil {
		t.Fatal(err)
	}
	db.Close()

	teams, err := extractTokensFromLevelDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 1 {
		t.Fatalf("expected 1 team (only xoxc-), got %d", len(teams))
	}
	if teams["T01ABC"].Token != "xoxc-workspace-token-1" {
		t.Errorf("got token %q, want %q", teams["T01ABC"].Token, "xoxc-workspace-token-1")
	}
	if teams["T01ABC"].Name != "Test Workspace" {
		t.Errorf("got name %q, want %q", teams["T01ABC"].Name, "Test Workspace")
	}
}

func TestExtractTokensFromLevelDB_V3(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "leveldb")

	config := map[string]any{
		"teams": map[string]any{
			"T01ABC": map[string]any{
				"token": "xoxc-v3-token",
				"name":  "V3 Workspace",
			},
		},
	}
	configJSON, _ := json.Marshal(config)

	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put([]byte("localConfig_v3"), configJSON, nil); err != nil {
		t.Fatal(err)
	}
	db.Close()

	teams, err := extractTokensFromLevelDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}
	if teams["T01ABC"].Token != "xoxc-v3-token" {
		t.Errorf("got token %q, want %q", teams["T01ABC"].Token, "xoxc-v3-token")
	}
}

func TestValidateDesktopCredentials_Success(t *testing.T) {
	var gotAuth, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     "https://acme.slack.com/",
			"team":    "Acme Corp",
			"team_id": "T01ABC",
			"user":    "tammer",
			"user_id": "U01XYZ",
		})
	}))
	defer srv.Close()

	ws, err := validateDesktopCredentials(context.Background(), http.DefaultClient, "xoxc-test-token", "xoxd-test-cookie", srv.URL+"/api/auth.test")
	if err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer xoxc-test-token" {
		t.Errorf("expected Authorization %q, got %q", "Bearer xoxc-test-token", gotAuth)
	}
	if gotCookie != "d=xoxd-test-cookie" {
		t.Errorf("expected Cookie %q, got %q", "d=xoxd-test-cookie", gotCookie)
	}
	if ws.BotToken != "xoxc-test-token" {
		t.Errorf("expected bot_token %q, got %q", "xoxc-test-token", ws.BotToken)
	}
	if ws.TeamID != "T01ABC" {
		t.Errorf("expected team_id %q, got %q", "T01ABC", ws.TeamID)
	}
}

func TestValidateDesktopCredentials_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "invalid_auth",
		})
	}))
	defer srv.Close()

	_, err := validateDesktopCredentials(context.Background(), http.DefaultClient, "xoxc-bad", "xoxd-bad", srv.URL+"/api/auth.test")
	if err == nil {
		t.Fatal("expected error for invalid auth")
	}
}

func TestDesktopLogin_NeedsPassword(t *testing.T) {
	t.Setenv("SLACK_SAFE_STORAGE_PASSWORD", "")

	_, err := DesktopLogin(context.Background(), DesktopLoginOptions{
		SlackDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error when password not set")
	}
}
