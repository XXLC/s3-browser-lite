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
