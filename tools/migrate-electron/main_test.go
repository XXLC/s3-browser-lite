package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"testing"
)

type dpapiGia struct{}

func (dpapiGia) Encrypt(b []byte) ([]byte, error) { return b, nil }
func (dpapiGia) Decrypt(b []byte) ([]byte, error) { return b, nil }

func mauV10(t *testing.T, key []byte, plain string) []byte {
	t.Helper()
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := bytes.Repeat([]byte{7}, 12)
	return append(append([]byte("v10"), nonce...), gcm.Seal(nil, nonce, []byte(plain), nil)...)
}

func TestGiaiMaChromiumVaLocalState(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	state, _ := json.Marshal(map[string]any{"os_crypt": map[string]string{
		"encrypted_key": base64.StdEncoding.EncodeToString(append([]byte("DPAPI"), key...)),
	}})
	got, err := chromiumKey(state, dpapiGia{})
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("khóa lấy từ Local State sai: %v", err)
	}
	plain, err := decryptSecret(got, base64.StdEncoding.EncodeToString(mauV10(t, key, "bí mật thử")))
	if err != nil || plain != "bí mật thử" {
		t.Fatalf("giải mã sai: %q %v", plain, err)
	}
}

func TestTuChoiBanMaHong(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	blob := mauV10(t, key, "x")
	blob[len(blob)-1] ^= 1
	if _, err := decryptChromium(key, blob); err == nil {
		t.Fatal("tag sai phải bị từ chối")
	}
	if _, err := decryptChromium(key, []byte("v20xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")); err == nil {
		t.Fatal("tiền tố lạ phải bị từ chối")
	}
	if _, err := chromiumKey([]byte(`{"os_crypt":{"encrypted_key":"AAAA"}}`), dpapiGia{}); err == nil {
		t.Fatal("khóa không có tiền tố DPAPI phải bị từ chối")
	}
}
