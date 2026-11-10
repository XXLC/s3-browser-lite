// Công cụ chuyển dữ liệu một lần: nhập hồ sơ kết nối (kèm khóa bí mật) từ bản Electron sang bản Lite.
//
// Bản Electron mã hóa khóa bí mật bằng safeStorage của Chromium: khóa AES-256-GCM nằm trong
// "Local State" (được DPAPI bảo vệ), mỗi bản mã có dạng "v10" + nonce 12 byte + dữ liệu + tag 16 byte.
// Công cụ giải mã rồi lưu bằng profiles.Store của bản Lite (mã hóa lại bằng DPAPI). Không in khóa.
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"s3browser/internal/profiles"
)

type electronProfile struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Endpoint         string `json:"endpoint"`
	Region           string `json:"region"`
	AccessKeyID      string `json:"accessKeyId"`
	SecretAccessKey  string `json:"secretAccessKey"`
	ForcePathStyle   *bool  `json:"forcePathStyle"`
	AllowInsecureTLS bool   `json:"allowInsecureTls"`
	DefaultBucket    string `json:"defaultBucket"`
}

// chromiumKey lấy khóa AES từ Local State: base64, bỏ tiền tố "DPAPI", rồi giải mã DPAPI.
func chromiumKey(localState []byte, dpapi profiles.Cipher) ([]byte, error) {
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(localState, &state); err != nil || state.OSCrypt.EncryptedKey == "" {
		return nil, errors.New("Local State không có os_crypt.encrypted_key")
	}
	blob, err := base64.StdEncoding.DecodeString(state.OSCrypt.EncryptedKey)
	if err != nil || len(blob) <= 5 || string(blob[:5]) != "DPAPI" {
		return nil, errors.New("encrypted_key không đúng định dạng DPAPI")
	}
	key, err := dpapi.Decrypt(blob[5:])
	if err != nil || len(key) != 32 {
		return nil, errors.New("không giải mã được khóa bằng DPAPI của tài khoản hiện tại")
	}
	return key, nil
}

// decryptChromium giải mã bản mã dạng "v10"/"v11" + nonce(12) + dữ liệu + tag(16).
func decryptChromium(key, blob []byte) ([]byte, error) {
	if len(blob) < 3+12+16 || (string(blob[:3]) != "v10" && string(blob[:3]) != "v11") {
		return nil, errors.New("bản mã không đúng định dạng v10")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, blob[3:15], blob[15:], nil)
}

func main() {
	from := flag.String("from", filepath.Join(os.Getenv("APPDATA"), "s3-browser"), "thư mục dữ liệu của bản Electron")
	dryRun := flag.Bool("dry-run", false, "chỉ giải mã và kiểm tra, không ghi vào bản Lite")
	flag.Parse()

	localState, err := os.ReadFile(filepath.Join(*from, "Local State"))
	fail(err, "không đọc được Local State")
	key, err := chromiumKey(localState, profiles.DPAPI{})
	fail(err, "lấy khóa giải mã")
	raw, err := os.ReadFile(filepath.Join(*from, "profiles.json"))
	fail(err, "không đọc được profiles.json")
	var records []electronProfile
	fail(json.Unmarshal(raw, &records), "profiles.json không hợp lệ")

	store, err := profiles.Default()
	fail(err, "mở kho hồ sơ của bản Lite")
	problems := 0
	for _, r := range records {
		line := fmt.Sprintf("- %s (%s): ", r.Name, r.Endpoint)
		secret, err := decryptSecret(key, r.SecretAccessKey)
		if err != nil {
			fmt.Println(line + "LỖI giải mã khóa bí mật, bỏ qua")
			problems++
			continue
		}
		if *dryRun {
			fmt.Println(line + "giải mã khóa bí mật thành công (chưa ghi)")
			continue
		}
		saved, err := store.Save(profiles.Profile{
			ID: r.ID, Name: r.Name, Endpoint: r.Endpoint, Region: r.Region, AccessKeyID: r.AccessKeyID,
			SecretAccessKey: secret, ForcePathStyle: r.ForcePathStyle, AllowInsecureTLS: r.AllowInsecureTLS, DefaultBucket: r.DefaultBucket,
		})
		if err != nil {
			fmt.Println(line + "LỖI khi lưu: " + err.Error())
			problems++
			continue
		}
		back, err := store.Get(saved.ID)
		if err != nil || subtle.ConstantTimeCompare([]byte(back.SecretAccessKey), []byte(secret)) != 1 {
			fmt.Println(line + "đã lưu nhưng đọc lại KHÔNG khớp")
			problems++
			continue
		}
		fmt.Println(line + "đã lưu, đọc lại khớp")
	}
	if problems > 0 {
		os.Exit(1)
	}
}

func decryptSecret(key []byte, encoded string) (string, error) {
	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	plain, err := decryptChromium(key, blob)
	return string(plain), err
}

func fail(err error, what string) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Lỗi:", what+":", err)
		os.Exit(2)
	}
}
