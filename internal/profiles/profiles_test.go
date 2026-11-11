package profiles_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"s3browser/internal/apperr"
	"s3browser/internal/profiles"
	"s3browser/internal/testutil"
)

func code(t *testing.T, err error, want string) {
	t.Helper()
	e, ok := err.(*apperr.Error)
	if !ok || e.Code != want {
		t.Fatalf("Mã lỗi không đúng: cần %s", want)
	}
}
func TestHồSơMãHóaVàKhôngLộRaGiaoDiện(t *testing.T) {
	store, input, _, dir := testutil.Fixture(t)
	saved, err := store.Save(input)
	if err != nil {
		t.Fatal("Không lưu được hồ sơ")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if strings.Contains(string(raw), input.SecretAccessKey) {
		t.Fatal("Tệp chứa dữ liệu rõ")
	}
	got, err := store.Get(saved.ID)
	if err != nil || got.SecretAccessKey != input.SecretAccessKey {
		t.Fatal("Giải mã không khớp")
	}
	list, _ := store.List()
	exported, _ := store.Export()
	for _, v := range []any{saved, list, json.RawMessage(exported)} {
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), input.SecretAccessKey) || strings.Contains(string(b), "secretAccessKey") {
			t.Fatal("Kết quả công khai chứa dữ liệu bí mật")
		}
	}
	if !saved.HasSecret || strings.Contains(exported, "hasSecret") {
		t.Fatal("Cờ khóa bí mật không đúng")
	}
}
func TestGiữVàThayKhóaBíMật(t *testing.T) {
	store, input, _, _ := testutil.Fixture(t)
	p, _ := store.Save(input)
	p.Name = "Tên đã đổi"
	if _, err := store.Save(p); err != nil {
		t.Fatal("Không sửa được hồ sơ")
	}
	got, _ := store.Get(p.ID)
	if got.SecretAccessKey != input.SecretAccessKey {
		t.Fatal("Không giữ khóa khi bỏ trống")
	}
	p.SecretAccessKey = testutil.Random()
	if _, err := store.Save(p); err != nil {
		t.Fatal("Không thay được khóa")
	}
	got, _ = store.Get(p.ID)
	if got.SecretAccessKey != p.SecretAccessKey {
		t.Fatal("Khóa mới không được giữ")
	}
}
func TestMãHóaLỗiKhôngThayTệp(t *testing.T) {
	store, input, c, dir := testutil.Fixture(t)
	saved, _ := store.Save(input)
	file := filepath.Join(dir, "profiles.json")
	before, _ := os.ReadFile(file)
	c.FailEncrypt = true
	input.ID = saved.ID
	_, err := store.Save(input)
	code(t, err, "EncryptionFailed")
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("Tệp đã bị thay khi mã hóa lỗi")
	}
	c.FailDecrypt = true
	_, err = store.Get(saved.ID)
	code(t, err, "DecryptionFailed")
	unavailable := profiles.New(file, nil)
	_, err = unavailable.Save(input)
	code(t, err, "EncryptionUnavailable")
	_, err = unavailable.Get(saved.ID)
	code(t, err, "EncryptionUnavailable")
}
func TestNhânBảnVàXóaHồSơ(t *testing.T) {
	store, input, _, _ := testutil.Fixture(t)
	saved, _ := store.Save(input)
	duplicate, err := store.Duplicate(saved.ID)
	if err != nil || duplicate.ID == saved.ID || !duplicate.HasSecret {
		t.Fatal("Nhân bản không đúng")
	}
	got, _ := store.Get(duplicate.ID)
	if got.SecretAccessKey != input.SecretAccessKey {
		t.Fatal("Bản sao mất khóa")
	}
	_, _ = store.Remove(duplicate.ID)
	list, _ := store.List()
	if len(list) != 1 {
		t.Fatal("Xóa ảnh hưởng bản gốc")
	}
}
func TestNhậpNguyênTửBỏKhóaVàCấpMãMới(t *testing.T) {
	store, input, _, dir := testutil.Fixture(t)
	saved, _ := store.Save(input)
	input.ID = saved.ID
	b, _ := json.Marshal([]profiles.Profile{input})
	added, err := store.Import(string(b))
	if err != nil || len(added) != 1 || added[0].ID == saved.ID || added[0].HasSecret || added[0].SecretAccessKey != "" {
		t.Fatal("Nhập hồ sơ không đúng")
	}
	file := filepath.Join(dir, "profiles.json")
	before, _ := os.ReadFile(file)
	_, err = store.Import(`[{"name":"Kho hợp lệ"},{"name":""}]`)
	if err == nil {
		t.Fatal("Đã nhận hồ sơ sai")
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("Đã ghi một phần danh sách nhập")
	}
	for _, input := range []string{`{}`, `null`, `[null]`, `{`, `[{"name":1}]`} {
		if _, err = store.Import(input); err == nil {
			t.Fatal("Đã nhận JSON nhập sai")
		}
	}
}
func TestChuẩnHóaVàKiểuHồSơ(t *testing.T) {
	for _, endpoint := range []string{"", "https://s3.example.invalid"} {
		p, err := profiles.Normalize(profiles.Profile{Name: " Kho ", Endpoint: endpoint})
		if err != nil || p.Region != "us-east-1" || p.PathStyle() != (endpoint != "") || p.AllowInsecureTLS || p.Name != "Kho" {
			t.Fatal("Mặc định không đúng")
		}
	}
	for _, input := range []profiles.Profile{{}, {Name: strings.Repeat("a", 201)}, {Name: "Kho", ID: "sai"}, {Name: "Kho", Endpoint: "file:///tmp"}, {Name: "Kho", Endpoint: "https://user@example.invalid"}, {Name: "Kho", Endpoint: "https://example.invalid?q=1"}, {Name: "Kho", Endpoint: "https://example.invalid#đoạn"}, {Name: "Kho", Region: "a\x00b"}} {
		if _, err := profiles.Normalize(input); err == nil {
			t.Fatal("Đã nhận hồ sơ không hợp lệ")
		}
	}
	for _, input := range []string{`null`, `[]`, `{"name":1}`, `{"name":"Kho","forcePathStyle":"true"}`, `{"name":"Kho","hasSecret":null}`, `{"name":"Kho","trườngLạ":1}`} {
		var p profiles.Profile
		if json.Unmarshal([]byte(input), &p) == nil {
			t.Fatal("Đã nhận trường sai kiểu")
		}
	}
}
func TestTệpHỏngVàGiớiHạnHồSơ(t *testing.T) {
	store, _, _, dir := testutil.Fixture(t)
	file := filepath.Join(dir, "profiles.json")
	for _, raw := range []string{"{", "null", "{}", `[{"name":""}]`} {
		_ = os.WriteFile(file, []byte(raw), 0600)
		_, err := store.List()
		code(t, err, "ProfileReadError")
	}
	_ = os.Remove(file)
	inputs := make([]map[string]string, 1000)
	for i := range inputs {
		inputs[i] = map[string]string{"name": "Kho"}
	}
	b, _ := json.Marshal(inputs)
	if _, err := store.Import(string(b)); err != nil {
		t.Fatal("Không nhập được giới hạn hợp lệ")
	}
	_, err := store.Save(profiles.Profile{Name: "Kho vượt giới hạn"})
	code(t, err, "ProfileLimit")
	_, err = store.Import(`[{"name":"Kho vượt giới hạn"}]`)
	code(t, err, "ProfileLimit")
}
func TestKhóaChủTệpVàBiếnMôiTrường(t *testing.T) {
	dir := testutil.Temporary(t)
	keyFile := filepath.Join(dir, "S3BrowserLite", "profiles.key")
	c := profiles.FileKeyCipher(func() (string, error) { return keyFile, nil })
	t.Setenv("S3BROWSER_MASTER_KEY", "")

	// Chỉ đọc (Decrypt) khi chưa có tệp khóa thì không được tạo tệp.
	if _, err := c.Decrypt([]byte("0123456789abcdef0123456789abcdef")); err == nil {
		t.Fatal("Giải mã khi chưa có khóa phải báo lỗi")
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatal("Chỉ đọc không được tạo tệp khóa")
	}

	// Mã hóa lần đầu phải tạo tệp khóa 32 byte và khứ hồi qua Store.
	store := profiles.New(filepath.Join(dir, "S3BrowserLite", "profiles.json"), c)
	secret := testutil.Random()
	saved, err := store.Save(profiles.Profile{Name: "Kho Linux", AccessKeyID: testutil.Random(), SecretAccessKey: secret})
	if err != nil {
		t.Fatalf("Không lưu được hồ sơ với khóa tệp: %v", err)
	}
	rawKey, err := os.ReadFile(keyFile)
	if err != nil || len(rawKey) != 32 {
		t.Fatalf("Tệp khóa không đúng 32 byte: len=%d, err=%v", len(rawKey), err)
	}
	got, err := store.Get(saved.ID)
	if err != nil || got.SecretAccessKey != secret {
		t.Fatalf("Không giải mã lại được hồ sơ: %v", err)
	}

	// Biến môi trường S3BROWSER_MASTER_KEY sai định dạng phải báo lỗi rõ ràng và không lưu.
	dirBad := testutil.Temporary(t)
	keyFileBad := filepath.Join(dirBad, "profiles.key")
	profilesFileBad := filepath.Join(dirBad, "profiles.json")
	cBad := profiles.FileKeyCipher(func() (string, error) { return keyFileBad, nil })
	storeBad := profiles.New(profilesFileBad, cBad)
	for _, bad := range []string{"không-phải-base64!!!", "dG9vLXNob3J0"} {
		t.Setenv("S3BROWSER_MASTER_KEY", bad)
		if _, err := cBad.Encrypt([]byte("bí-mật")); err == nil || !strings.Contains(err.Error(), "S3BROWSER_MASTER_KEY") {
			t.Fatalf("Khóa chủ sai định dạng %q phải báo lỗi rõ ràng, nhận: %v", bad, err)
		}
		if _, err := storeBad.Save(profiles.Profile{Name: "Kho lỗi", SecretAccessKey: secret}); err == nil {
			t.Fatal("Không được lưu hồ sơ khi S3BROWSER_MASTER_KEY sai định dạng")
		}
		if _, err := os.Stat(profilesFileBad); !os.IsNotExist(err) {
			t.Fatal("Đã ghi tệp hồ sơ khi khóa chủ sai")
		}
		if _, err := os.Stat(keyFileBad); !os.IsNotExist(err) {
			t.Fatal("Đã tạo tệp khóa khi đã đặt biến môi trường")
		}
	}

	// Biến môi trường S3BROWSER_MASTER_KEY hợp lệ (32 byte base64 chuẩn) dùng trực tiếp, không tạo tệp khóa.
	t.Setenv("S3BROWSER_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	savedEnv, err := storeBad.Save(profiles.Profile{Name: "Kho biến môi trường", SecretAccessKey: secret})
	if err != nil {
		t.Fatalf("Không lưu được hồ sơ với S3BROWSER_MASTER_KEY hợp lệ: %v", err)
	}
	if _, err := os.Stat(keyFileBad); !os.IsNotExist(err) {
		t.Fatal("Có S3BROWSER_MASTER_KEY thì không được tạo tệp profiles.key")
	}
	gotEnv, err := storeBad.Get(savedEnv.ID)
	if err != nil || gotEnv.SecretAccessKey != secret {
		t.Fatalf("Giải mã bằng S3BROWSER_MASTER_KEY không khớp: %v", err)
	}
}
