package testutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"s3browser/internal/profiles"
)

func Temporary(t testing.TB) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal("Không đọc được thư mục kiểm thử")
	}
	for {
		if _, err = os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("Không tìm thấy gốc dự án")
		}
		root = parent
	}
	base := filepath.Join(root, ".gocache", "kiểm-thử")
	if err = os.MkdirAll(base, 0700); err != nil {
		t.Fatal("Không tạo được thư mục kiểm thử")
	}
	dir, err := os.MkdirTemp(base, "lần-")
	if err != nil {
		t.Fatal("Không tạo được thư mục tạm")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error("Không dọn được thư mục kiểm thử")
		}
	})
	return dir
}
func Random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("Không sinh được dữ liệu kiểm thử")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

type Cipher struct {
	aead        cipher.AEAD
	FailEncrypt bool
	FailDecrypt bool
}

func NewCipher(t testing.TB) *Cipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal("Không sinh được khóa kiểm thử")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal("Không tạo được bộ mã hóa kiểm thử")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal("Không tạo được bộ mã hóa kiểm thử")
	}
	return &Cipher{aead: aead}
}
func (c *Cipher) Encrypt(b []byte) ([]byte, error) {
	if c.FailEncrypt {
		return nil, os.ErrPermission
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, b, nil), nil
}
func (c *Cipher) Decrypt(b []byte) ([]byte, error) {
	if c.FailDecrypt || len(b) < c.aead.NonceSize() {
		return nil, os.ErrPermission
	}
	return c.aead.Open(nil, b[:c.aead.NonceSize()], b[c.aead.NonceSize():], nil)
}
func Fixture(t testing.TB) (*profiles.Store, profiles.Profile, *Cipher, string) {
	t.Helper()
	dir := Temporary(t)
	c := NewCipher(t)
	store := profiles.New(filepath.Join(dir, "profiles.json"), c)
	p := profiles.Profile{Name: "Kho kiểm thử", Endpoint: "https://s3.example.invalid", AccessKeyID: Random(), SecretAccessKey: Random()}
	return store, p, c, dir
}
