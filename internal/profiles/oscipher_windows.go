package profiles

// osCipher là kho mã hóa của hệ điều hành: DPAPI trên Windows.
func osCipher() Cipher { return DPAPI{} }
