package profiles

import (
	"bytes"
	"errors"
	"testing"
)

func newTestCipher(key []byte, err error) aesCipher {
	return aesCipher{keyFn: func(bool) ([]byte, error) { return key, err }}
}

func TestAESCipherKhuHoiDuLieu(t *testing.T) {
	c := newTestCipher(bytes.Repeat([]byte{7}, 32), nil)
	plain := []byte("khoa-bi-mat")
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, plain) {
		t.Fatal("bản mã chứa dữ liệu rõ")
	}
	enc2, _ := c.Encrypt(plain)
	if bytes.Equal(enc, enc2) {
		t.Fatal("hai lần mã hóa phải khác nonce")
	}
	got, err := c.Decrypt(enc)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("giải mã sai: %q %v", got, err)
	}
}

func TestAESCipherTuChoiBanMaHong(t *testing.T) {
	c := newTestCipher(bytes.Repeat([]byte{7}, 32), nil)
	enc, _ := c.Encrypt([]byte("abc"))
	enc[len(enc)-1] ^= 1
	if _, err := c.Decrypt(enc); err == nil {
		t.Fatal("bản mã bị sửa phải bị từ chối")
	}
	if _, err := c.Decrypt([]byte("ngan")); err == nil {
		t.Fatal("bản mã quá ngắn phải bị từ chối")
	}
	other := newTestCipher(bytes.Repeat([]byte{9}, 32), nil)
	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("sai khóa phải bị từ chối")
	}
}

func TestAESCipherLoiKhoa(t *testing.T) {
	c := newTestCipher(nil, errors.New("keychain khóa"))
	if _, err := c.Encrypt([]byte("x")); err == nil {
		t.Fatal("lỗi khóa phải được trả về")
	}
}
