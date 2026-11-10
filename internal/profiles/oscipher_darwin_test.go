package profiles

import (
	"bytes"
	"testing"
)

// Thử thật với Keychain đăng nhập; bỏ qua nếu môi trường (ví dụ CI không có Keychain mở) không cho phép.
func TestKeychainKhuHoiDuLieu(t *testing.T) {
	c := osCipher()
	plain := []byte("khoa-bi-mat-kiem-thu")
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Skipf("Keychain không khả dụng trong môi trường này: %v", err)
	}
	got, err := c.Decrypt(enc)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("giải mã sai: %q %v", got, err)
	}
}
