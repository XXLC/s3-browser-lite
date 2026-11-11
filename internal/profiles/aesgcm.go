package profiles

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// aesCipher mã hóa bằng AES-256-GCM với khóa chủ do keyFn cung cấp (ví dụ lấy từ Keychain).
// Bản mã là nonce (12 byte) nối với dữ liệu đã niêm phong.
type aesCipher struct {
	// keyFn trả khóa 32 byte; create=true cho phép tạo khóa mới nếu chưa có.
	keyFn func(create bool) ([]byte, error)
}

func (c aesCipher) aead(create bool) (cipher.AEAD, error) {
	key, err := c.keyFn(create)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c aesCipher) Encrypt(data []byte) ([]byte, error) {
	gcm, err := c.aead(true)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, data, nil), nil
}

func (c aesCipher) Decrypt(data []byte) ([]byte, error) {
	gcm, err := c.aead(false)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("bản mã quá ngắn")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
}

// FileKeyCipher mã hóa bằng AES-256-GCM với khóa chủ từ S3BROWSER_MASTER_KEY hoặc tệp khóa tại keyPath.
func FileKeyCipher(keyPath func() (string, error)) Cipher {
	var mu sync.Mutex
	return aesCipher{keyFn: func(create bool) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if raw, ok := os.LookupEnv("S3BROWSER_MASTER_KEY"); ok && raw != "" {
			key, err := base64.StdEncoding.DecodeString(raw)
			if err != nil || len(key) != 32 {
				return nil, errors.New("S3BROWSER_MASTER_KEY phải là chuỗi base64 chuẩn của đúng 32 byte")
			}
			return key, nil
		}
		path, err := keyPath()
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if err == nil {
			if len(b) != 32 {
				return nil, errors.New("tệp khóa profiles.key không hợp lệ")
			}
			return b, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		if !create {
			return nil, errors.New("chưa có tệp khóa mã hóa hồ sơ")
		}
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		f, err := os.CreateTemp(dir, ".profiles-key-*.tmp")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		if err = f.Chmod(0600); err == nil {
			_, err = f.Write(key)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(f.Name(), path)
		}
		if err != nil {
			return nil, err
		}
		return key, nil
	}}
}
