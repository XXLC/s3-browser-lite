//go:build !windows && !darwin

package profiles

import (
	"os"
	"path/filepath"
)

// osCipher mã hóa khóa bí mật bằng AES-256-GCM với khóa chủ từ S3BROWSER_MASTER_KEY hoặc tệp profiles.key.
func osCipher() Cipher {
	return FileKeyCipher(func() (string, error) {
		root, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "S3BrowserLite", "profiles.key"), nil
	})
}
