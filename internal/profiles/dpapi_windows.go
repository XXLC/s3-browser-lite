package profiles

import (
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

type DPAPI struct{}

func (DPAPI) Encrypt(data []byte) ([]byte, error) { return protect(data, false) }
func (DPAPI) Decrypt(data []byte) ([]byte, error) { return protect(data, true) }
func protect(data []byte, decrypt bool) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(data))}
	if len(data) > 0 {
		in.Data = &data[0]
	}
	var out windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	runtime.KeepAlive(data)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
