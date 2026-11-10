//go:build !windows && !darwin

package profiles

// osCipher chưa có kho mã hóa hệ điều hành trên nền tảng này; DPAPI ở đây từ chối mọi thao tác.
func osCipher() Cipher { return DPAPI{} }
