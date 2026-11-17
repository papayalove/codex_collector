package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Resolve picks the collector user id.
// A configured email becomes SHA-256 hex. Otherwise the Codex account id is used.
// If neither exists, the device id is used.
func Resolve(email, accountID, deviceID string) (id, source string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email != "" {
		sum := sha256.Sum256([]byte(email))
		return hex.EncodeToString(sum[:]), "email"
	}
	accountID = strings.TrimSpace(accountID)
	if usableID(accountID) {
		return accountID, "account"
	}
	return strings.TrimSpace(deviceID), "device"
}

func usableID(s string) bool {
	if s == "" || len(s) > 128 || strings.Contains(s, " ") {
		return false
	}
	if strings.HasPrefix(s, "sk-") || strings.HasPrefix(s, "eyJ") {
		return false
	}
	return true
}

// AccountID reads the Codex account id from auth.json. Token fields are ignored.
func AccountID(codexHome string) string {
	b, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		return ""
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	allow := map[string]bool{
		"account_id": true, "accountId": true, "chatgpt_account_id": true,
		"user_id": true, "userId": true,
	}
	return findID(v, allow)
}

func findID(v any, allow map[string]bool) string {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.Contains(lk, "password") {
				continue
			}
			if allow[k] {
				if s, ok := child.(string); ok && usableID(s) {
					return s
				}
			}
			if s := findID(child, allow); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range t {
			if s := findID(child, allow); s != "" {
				return s
			}
		}
	}
	return ""
}
