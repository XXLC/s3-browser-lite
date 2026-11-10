package profiles

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

const (
	keychainService = "S3BrowserLite"
	keychainAccount = "profiles-key"
	// Mã thoát của `security` khi không thấy mục trong Keychain (errSecItemNotFound).
	securityNotFound = 44
)

// osCipher mã hóa khóa bí mật bằng AES-256-GCM; khóa chủ nằm trong Keychain đăng nhập của người dùng.
func osCipher() Cipher {
	var (
		mu  sync.Mutex
		key []byte
	)
	return aesCipher{keyFn: func(create bool) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if key != nil {
			return key, nil
		}
		k, err := keychainKey(create)
		if err == nil {
			key = k
		}
		return k, err
	}}
}

func keychainKey(create bool) ([]byte, error) {
	out, code, err := runSecurity("find-generic-password", "-s", keychainService, "-a", keychainAccount, "-w")
	if err == nil {
		k, e := hex.DecodeString(strings.TrimSpace(out))
		if e != nil || len(k) != 32 {
			return nil, errors.New("khóa trong Keychain không hợp lệ")
		}
		return k, nil
	}
	if code != securityNotFound {
		return nil, fmt.Errorf("không đọc được Keychain: %w", err)
	}
	if !create {
		return nil, errors.New("Keychain chưa có khóa mã hóa hồ sơ")
	}
	k := make([]byte, 32)
	if _, e := rand.Read(k); e != nil {
		return nil, e
	}
	// Dùng `security -i` để khóa đi qua stdin, không lộ trong danh sách tiến trình.
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", keychainService, keychainAccount, hex.EncodeToString(k)))
	if e := cmd.Run(); e != nil {
		return nil, fmt.Errorf("không ghi được Keychain: %w", e)
	}
	return k, nil
}

func runSecurity(args ...string) (string, int, error) {
	var stdout bytes.Buffer
	cmd := exec.Command("/usr/bin/security", args...)
	cmd.Stdout = &stdout
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	return stdout.String(), code, err
}
