package profiles

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
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
