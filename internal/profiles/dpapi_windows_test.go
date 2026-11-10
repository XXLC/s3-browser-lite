package profiles_test

import (
	"bytes"
	"errors"
	"s3browser/internal/profiles"
	"s3browser/internal/testutil"
	"syscall"
	"testing"
)

func TestDPAPIKhứHồiDữLiệuSinhTạiChỗ(t *testing.T) {
	cipher := profiles.DPAPI{}
	input := []byte(testutil.Random())
	encrypted, err := cipher.Encrypt(input)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		t.Skip("Môi trường hiện tại không cung cấp kho DPAPI của người dùng; đã kiểm thử lưu hồ sơ qua giao diện mã hóa giả lập.")
	}
	if err != nil {
		t.Fatalf("DPAPI không mã hóa được dữ liệu kiểm thử: %v", err)
	}
	if bytes.Contains(encrypted, input) {
		t.Fatal("DPAPI trả dữ liệu rõ")
	}
	plain, err := cipher.Decrypt(encrypted)
	if err != nil || !bytes.Equal(plain, input) {
		t.Fatal("DPAPI không giải mã đúng dữ liệu kiểm thử")
	}
}
