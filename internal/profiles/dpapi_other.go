//go:build !windows

package profiles

import "s3browser/internal/apperr"

type DPAPI struct{}

func (DPAPI) Encrypt([]byte) ([]byte, error) {
	return nil, apperr.New("EncryptionUnavailable", "DPAPI chỉ khả dụng trên Windows.")
}
func (DPAPI) Decrypt([]byte) ([]byte, error) {
	return nil, apperr.New("EncryptionUnavailable", "DPAPI chỉ khả dụng trên Windows.")
}
