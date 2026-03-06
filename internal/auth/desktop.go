package auth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"golang.org/x/crypto/pbkdf2"

	_ "modernc.org/sqlite"
)

// DesktopLoginOptions configures DesktopLogin behavior.
type DesktopLoginOptions struct {
	// SlackDir overrides the Slack Desktop data directory.
	// Default: ~/Library/Application Support/Slack
	SlackDir string

	// HTTPClient is used for auth.test validation calls.
	// Should include Chrome TLS fingerprint and user-agent.
	// Falls back to http.DefaultClient if nil.
	HTTPClient *http.Client

	// StatusFunc is called with status messages for the user.
	StatusFunc func(string)
}

// DesktopLogin extracts Slack credentials from the Slack Desktop app.
//
// Reads tokens from LevelDB (Local Storage) and decrypts the d cookie
// from the Cookies SQLite database using the Slack Safe Storage password
// from macOS Keychain (provided via SLACK_SAFE_STORAGE_PASSWORD).
func DesktopLogin(ctx context.Context, opts DesktopLoginOptions) ([]WorkspaceCredentials, error) {
	status := opts.StatusFunc
	if status == nil {
		status = func(string) {}
	}

	password := os.Getenv("SLACK_SAFE_STORAGE_PASSWORD")
	if password == "" {
		return nil, fmt.Errorf("SLACK_SAFE_STORAGE_PASSWORD not set; extract it from Keychain Access (search for 'Slack Safe Storage')")
	}

	slackDir := opts.SlackDir
	if slackDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		slackDir = filepath.Join(home, "Library", "Application Support", "Slack")
	}

	// Copy LevelDB to a temp dir to avoid lock contention with running Slack.
	status("Copying Slack local storage...")
	levelDBPath := filepath.Join(slackDir, "Local Storage", "leveldb")
	tmpDB, err := copyDir(levelDBPath)
	if err != nil {
		return nil, fmt.Errorf("copying LevelDB: %w", err)
	}
	defer os.RemoveAll(tmpDB)

	status("Extracting tokens...")
	teams, err := extractTokensFromLevelDB(tmpDB)
	if err != nil {
		return nil, fmt.Errorf("extracting tokens: %w", err)
	}

	status("Decrypting cookie...")
	cookiesPath := filepath.Join(slackDir, "Cookies")
	cookie, err := extractCookie(cookiesPath, password)
	if err != nil {
		return nil, fmt.Errorf("extracting cookie: %w", err)
	}

	status(fmt.Sprintf("Found %d workspace(s). Validating...", len(teams)))

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	var results []WorkspaceCredentials
	for teamID, team := range teams {
		ws, err := validateDesktopCredentials(ctx, httpClient, team.Token, cookie, "https://slack.com/api/auth.test")
		if err != nil {
			status(fmt.Sprintf("Warning: workspace %s (%s) failed validation: %v", team.Name, teamID, err))
			continue
		}
		ws.Cookie = cookie
		ws.AuthMethod = "desktop"
		results = append(results, *ws)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no valid workspaces found after validation")
	}

	return results, nil
}

// desktopTeam represents a team entry from Slack's localConfig_v2/v3.
type desktopTeam struct {
	Token string `json:"token"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}

// extractTokensFromLevelDB reads workspace tokens from Slack's LevelDB.
// Chromium prefixes localStorage keys with the origin and separator bytes,
// so we scan all keys for ones containing "localConfig_v2" or "localConfig_v3".
func extractTokensFromLevelDB(dbPath string) (map[string]desktopTeam, error) {
	db, err := leveldb.OpenFile(dbPath, &opt.Options{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("opening LevelDB: %w", err)
	}
	defer db.Close()

	var configData []byte
	iter := db.NewIterator(nil, nil)
	defer iter.Release()
	for iter.Next() {
		key := string(iter.Key())
		if strings.Contains(key, "localConfig_v3") || strings.Contains(key, "localConfig_v2") {
			configData = iter.Value()
			break
		}
	}
	if configData == nil {
		return nil, fmt.Errorf("localConfig_v2/v3 not found in LevelDB")
	}

	// Chromium LevelDB values may have a binary prefix before the JSON.
	jsonStart := bytes.IndexByte(configData, '{')
	if jsonStart < 0 {
		return nil, fmt.Errorf("no JSON found in localConfig value")
	}
	configData = configData[jsonStart:]

	var config struct {
		Teams map[string]desktopTeam `json:"teams"`
	}
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("parsing localConfig: %w", err)
	}

	// Filter to xoxc- tokens only.
	result := make(map[string]desktopTeam)
	for k, t := range config.Teams {
		if strings.HasPrefix(t.Token, "xoxc-") {
			result[k] = t
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no xoxc- tokens found in localConfig")
	}

	return result, nil
}

// extractCookie reads and decrypts the d cookie from Slack's Cookies SQLite DB.
func extractCookie(cookiesPath, password string) (string, error) {
	// Copy the cookies file to avoid SQLite lock issues.
	tmpFile, err := copyFile(cookiesPath)
	if err != nil {
		return "", fmt.Errorf("copying cookies DB: %w", err)
	}
	defer os.Remove(tmpFile)

	db, err := sql.Open("sqlite", tmpFile+"?mode=ro")
	if err != nil {
		return "", fmt.Errorf("opening cookies DB: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(
		"SELECT value, encrypted_value FROM cookies WHERE name='d' AND host_key LIKE '%slack.com' ORDER BY length(encrypted_value) DESC",
	)
	if err != nil {
		return "", fmt.Errorf("querying cookies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var plainValue string
		var encryptedValue []byte
		if err := rows.Scan(&plainValue, &encryptedValue); err != nil {
			continue
		}

		// Try plaintext first.
		if plainValue != "" && strings.HasPrefix(plainValue, "xoxd-") {
			return plainValue, nil
		}

		// Decrypt.
		if len(encryptedValue) > 0 {
			decrypted, err := decryptCookie(encryptedValue, password)
			if err != nil {
				continue
			}
			// Extract xoxd- token from decrypted value.
			// Keep the URL-encoded form - Slack requires it.
			re := regexp.MustCompile(`xoxd-[A-Za-z0-9%/+_=.-]+`)
			m := re.FindString(decrypted)
			if m != "" {
				return m, nil
			}
		}
	}

	return "", fmt.Errorf("d cookie not found in Slack cookies database")
}

// decryptCookie decrypts a Chrome/Electron Safe Storage encrypted value.
// Format: "v10" prefix + AES-CBC ciphertext.
// Key: PBKDF2(password, "saltysalt", 1003, 16 bytes, SHA1).
// IV: 16 bytes of 0x20 (space).
func decryptCookie(encrypted []byte, password string) (string, error) {
	// Strip v10/v11 prefix.
	if len(encrypted) >= 3 && (string(encrypted[:3]) == "v10" || string(encrypted[:3]) == "v11") {
		encrypted = encrypted[3:]
	}

	key := pbkdf2.Key([]byte(password), []byte("saltysalt"), 1003, 16, sha1.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	if len(encrypted)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ciphertext is not a multiple of the block size")
	}

	iv := make([]byte, aes.BlockSize)
	for i := range iv {
		iv[i] = ' '
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(encrypted))
	mode.CryptBlocks(plaintext, encrypted)

	// PKCS7 unpad.
	if len(plaintext) == 0 {
		return "", fmt.Errorf("empty plaintext after decryption")
	}
	padLen := int(plaintext[len(plaintext)-1])
	if padLen < 1 || padLen > aes.BlockSize {
		return "", fmt.Errorf("invalid PKCS7 padding")
	}
	for i := len(plaintext) - padLen; i < len(plaintext); i++ {
		if plaintext[i] != byte(padLen) {
			return "", fmt.Errorf("invalid PKCS7 padding")
		}
	}
	plaintext = plaintext[:len(plaintext)-padLen]

	return string(plaintext), nil
}

// validateDesktopCredentials calls auth.test with the given token and cookie
// to get workspace metadata. The endpoint parameter allows testing with httptest.
func validateDesktopCredentials(ctx context.Context, client *http.Client, token, cookie, endpoint string) (*WorkspaceCredentials, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "d="+cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
		URL    string `json:"url"`
		Team   string `json:"team"`
		TeamID string `json:"team_id"`
		User   string `json:"user"`
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("auth.test failed: %s", result.Error)
	}

	return &WorkspaceCredentials{
		BotToken: token,
		TeamID:   result.TeamID,
		TeamName: result.Team,
		UserID:   result.UserID,
	}, nil
}

// copyDir copies a directory recursively.
// Returns the path to the temporary copy.
func copyDir(src string) (string, error) {
	tmp, err := os.MkdirTemp("", "slack-leveldb-*")
	if err != nil {
		return "", err
	}
	cmd := exec.Command("cp", "-R", src+"/.", tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("cp: %s: %w", out, err)
	}
	// Remove LOCK file so goleveldb can open it.
	os.Remove(filepath.Join(tmp, "LOCK"))
	return tmp, nil
}

// copyFile copies a single file to a temp location.
func copyFile(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "slack-cookies-*")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}
