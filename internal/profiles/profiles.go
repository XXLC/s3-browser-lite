package profiles

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"s3browser/internal/apperr"
	"s3browser/internal/validate"
)

type Profile struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Endpoint         string `json:"endpoint"`
	Region           string `json:"region"`
	AccessKeyID      string `json:"accessKeyId"`
	SecretAccessKey  string `json:"secretAccessKey,omitempty"`
	HasSecret        bool   `json:"hasSecret"`
	ForcePathStyle   *bool  `json:"forcePathStyle,omitempty"`
	AllowInsecureTLS bool   `json:"allowInsecureTls"`
	DefaultBucket    string `json:"defaultBucket"`
}

func (p Profile) PathStyle() bool { return p.ForcePathStyle != nil && *p.ForcePathStyle }
func Normalize(p Profile) (Profile, error) {
	for _, field := range []struct {
		value string
		max   int
	}{{p.Name, 200}, {p.Endpoint, 2048}, {p.Region, 128}, {p.ID, 36}, {p.AccessKeyID, 1024}, {p.SecretAccessKey, 8192}, {p.DefaultBucket, 255}} {
		if validate.Text(field.value, "Hồ sơ", field.max, true) != nil {
			return Profile{}, apperr.New("InvalidProfile", "Trường hồ sơ không hợp lệ hoặc quá dài.")
		}
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Endpoint = strings.TrimSpace(p.Endpoint)
	p.Region = strings.TrimSpace(p.Region)
	if p.Name == "" {
		return Profile{}, apperr.New("InvalidProfile", "Vui lòng nhập tên hồ sơ.")
	}
	if p.Endpoint != "" {
		u, err := url.Parse(p.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(p.Endpoint, "#") {
			return Profile{}, apperr.New("InvalidProfile", "Endpoint phải dùng HTTP hoặc HTTPS, không chứa thông tin đăng nhập, truy vấn hoặc đoạn neo.")
		}
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	} else if validate.ID(p.ID) != nil {
		return Profile{}, apperr.New("InvalidProfile", "Mã hồ sơ phải là UUID.")
	}
	if p.Region == "" {
		p.Region = "us-east-1"
	}
	if p.ForcePathStyle == nil {
		v := p.Endpoint != ""
		p.ForcePathStyle = &v
	}
	return p, nil
}

type Cipher interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}
type Store struct {
	mu     sync.Mutex
	file   string
	cipher Cipher
}

func New(file string, cipher Cipher) *Store { return &Store{file: file, cipher: cipher} }
func Default() (*Store, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, apperr.New("ProfileReadError", "Không xác định được thư mục dữ liệu ứng dụng.")
	}
	return New(filepath.Join(root, "S3BrowserLite", "profiles.json"), osCipher()), nil
}
func public(p Profile) Profile {
	p.HasSecret = p.SecretAccessKey != ""
	p.SecretAccessKey = ""
	return p
}
func (s *Store) records() ([]Profile, error) {
	b, err := os.ReadFile(s.file)
	if os.IsNotExist(err) {
		return []Profile{}, nil
	}
	if err != nil {
		return nil, apperr.New("ProfileReadError", "Không đọc được tệp hồ sơ. Kiểm tra định dạng và quyền truy cập.")
	}
	var records []Profile
	if json.Unmarshal(b, &records) != nil || records == nil || len(records) > 1000 {
		return nil, apperr.New("ProfileReadError", "Không đọc được tệp hồ sơ. Kiểm tra định dạng và quyền truy cập.")
	}
	for i, p := range records {
		encrypted := p.SecretAccessKey
		p.SecretAccessKey = ""
		normalized, err := Normalize(p)
		if err != nil {
			return nil, apperr.New("ProfileReadError", "Tệp hồ sơ chứa cấu hình không hợp lệ.")
		}
		normalized.SecretAccessKey = encrypted
		records[i] = normalized
	}
	return records, nil
}
func (s *Store) write(records []Profile) error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0700); err != nil {
		return apperr.New("ProfileWriteError", "Không tạo được thư mục dữ liệu ứng dụng.")
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return apperr.New("ProfileWriteError", "Không chuyển được hồ sơ sang JSON.")
	}
	f, err := os.CreateTemp(filepath.Dir(s.file), ".profiles-*.tmp")
	if err != nil {
		return apperr.New("ProfileWriteError", "Không tạo được tệp hồ sơ tạm.")
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), s.file)
	}
	if err != nil {
		return apperr.New("ProfileWriteError", "Không lưu được hồ sơ. Kiểm tra quyền ghi thư mục dữ liệu ứng dụng.")
	}
	return nil
}
func (s *Store) List() ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.records()
	if err != nil {
		return nil, err
	}
	for i := range records {
		records[i] = public(records[i])
	}
	return records, nil
}
func (s *Store) Get(id string) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.records()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range records {
		if p.ID != id {
			continue
		}
		if p.SecretAccessKey != "" {
			if s.cipher == nil {
				return Profile{}, apperr.New("EncryptionUnavailable", "Kho mã hóa hệ điều hành chưa khả dụng.")
			}
			raw, e := base64.StdEncoding.DecodeString(p.SecretAccessKey)
			if e != nil {
				return Profile{}, apperr.New("DecryptionFailed", "Bản mã khóa bí mật không hợp lệ.")
			}
			plain, e := s.cipher.Decrypt(raw)
			if e != nil {
				return Profile{}, apperr.New("DecryptionFailed", "Không giải mã được khóa bí mật bằng tài khoản hệ điều hành hiện tại.")
			}
			p.SecretAccessKey = string(plain)
		}
		return Normalize(p)
	}
	return Profile{}, apperr.New("ProfileNotFound", "Không tìm thấy hồ sơ kết nối.")
}
func (s *Store) Save(p Profile) (Profile, error) {
	p, err := Normalize(p)
	if err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.records()
	if err != nil {
		return Profile{}, err
	}
	index := -1
	for i := range records {
		if records[i].ID == p.ID {
			index = i
			break
		}
	}
	if index < 0 && len(records) >= 1000 {
		return Profile{}, apperr.New("ProfileLimit", "Chỉ hỗ trợ tối đa 1000 hồ sơ.")
	}
	if p.SecretAccessKey != "" {
		if s.cipher == nil {
			return Profile{}, apperr.New("EncryptionUnavailable", "Kho mã hóa hệ điều hành không khả dụng. Ứng dụng từ chối lưu khóa bí mật dạng rõ.")
		}
		encrypted, e := s.cipher.Encrypt([]byte(p.SecretAccessKey))
		if e != nil || len(encrypted) == 0 {
			return Profile{}, apperr.New("EncryptionFailed", "Không mã hóa được khóa bí mật. Hồ sơ chưa được lưu.")
		}
		p.SecretAccessKey = base64.StdEncoding.EncodeToString(encrypted)
	} else if index >= 0 {
		p.SecretAccessKey = records[index].SecretAccessKey
	}
	p.HasSecret = false
	if index >= 0 {
		records[index] = p
	} else {
		records = append(records, p)
	}
	if err = s.write(records); err != nil {
		return Profile{}, err
	}
	return public(p), nil
}
func (s *Store) Remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.records()
	if err != nil {
		return false, err
	}
	kept := make([]Profile, 0, len(records))
	for _, p := range records {
		if p.ID != id {
			kept = append(kept, p)
		}
	}
	err = s.write(kept)
	return err == nil, err
}
func (s *Store) Duplicate(id string) (Profile, error) {
	p, err := s.Get(id)
	if err != nil {
		return Profile{}, err
	}
	p.ID = ""
	p.Name += " (bản sao)"
	return s.Save(p)
}
func (s *Store) Import(input string) ([]Profile, error) {
	var raw []map[string]json.RawMessage
	if validate.Length(input) > 2*1024*1024 || json.Unmarshal([]byte(input), &raw) != nil || raw == nil || len(raw) > 1000 {
		return nil, apperr.New("InvalidImport", "Danh sách nhập phải là JSON có tối đa 1000 hồ sơ.")
	}
	added := make([]Profile, 0, len(raw))
	for _, fields := range raw {
		for name := range fields {
			switch name {
			case "name", "endpoint", "region", "accessKeyId", "forcePathStyle", "allowInsecureTls", "defaultBucket":
			default:
				delete(fields, name)
			}
		}
		b, _ := json.Marshal(fields)
		var p Profile
		if json.Unmarshal(b, &p) != nil {
			return nil, apperr.New("InvalidProfile", "Trường hồ sơ không đúng kiểu dữ liệu.")
		}
		p, err := Normalize(p)
		if err != nil {
			return nil, err
		}
		added = append(added, p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.records()
	if err != nil {
		return nil, err
	}
	if len(records)+len(added) > 1000 {
		return nil, apperr.New("ProfileLimit", "Tổng số hồ sơ vượt quá 1000.")
	}
	if err = s.write(append(records, added...)); err != nil {
		return nil, err
	}
	return added, nil
}
func (s *Store) Export() (string, error) {
	records, err := s.List()
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(records)
	var fields []map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	for _, f := range fields {
		delete(f, "hasSecret")
	}
	b, err = json.MarshalIndent(fields, "", "  ")
	return string(b), err
}

// UnmarshalJSON từ chối trường lạ và null để không biến dữ liệu sai kiểu thành mặc định.
func (p *Profile) UnmarshalJSON(b []byte) error {
	type plain Profile
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return apperr.New("InvalidProfile", "Hồ sơ phải là một đối tượng.")
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return apperr.New("InvalidProfile", "Trường hồ sơ không được là null.")
		}
	}
	var out plain
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil {
		return apperr.New("InvalidProfile", "Trường hồ sơ không được hỗ trợ hoặc sai kiểu dữ liệu.")
	}
	*p = Profile(out)
	return nil
}
