package apperr

import (
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/aws/smithy-go"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string       { b, _ := json.Marshal(e); return string(b) }
func New(code, message string) error { return &Error{code, message} }

// Filter giữ cả khóa cũ để lỗi từ tác vụ đang kết thúc vẫn được che.
type Filter struct {
	mu     sync.RWMutex
	values map[string]struct{}
}

func (f *Filter) Remember(values ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.values == nil {
		f.values = map[string]struct{}{}
	}
	for _, v := range values {
		if v != "" {
			f.values[v] = struct{}{}
		}
	}
}
func (f *Filter) Describe(err error) *Error {
	if err == nil {
		return nil
	}
	code, message := "UnknownError", err.Error()
	var own *Error
	var api smithy.APIError
	if errors.As(err, &own) {
		code, message = own.Code, own.Message
	} else if errors.As(err, &api) {
		code, message = api.ErrorCode(), api.ErrorMessage()
	}
	f.mu.RLock()
	values := make([]string, 0, len(f.values))
	for v := range f.values {
		values = append(values, v)
	}
	f.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, v := range values {
		for _, candidate := range []string{v, url.QueryEscape(v), strings.ReplaceAll(url.QueryEscape(v), "+", "%20"), url.PathEscape(v), encodeURIComponent(v)} {
			code = strings.ReplaceAll(code, candidate, "[đã ẩn]")
			message = strings.ReplaceAll(message, candidate, "[đã ẩn]")
		}
	}
	hint := hints[code]
	if hint == "" && (strings.Contains(code, "TLS") || strings.Contains(code, "CERT")) {
		hint = "Kiểm tra chứng chỉ TLS của endpoint."
	}
	if hint == "" && own == nil {
		hint = "Kiểm tra endpoint, vùng và forcePathStyle. CORS không áp dụng vì ứng dụng gọi S3 từ tiến trình chính."
	}
	if hint != "" {
		message += " " + hint
	}
	return &Error{code, message}
}

func encodeURIComponent(value string) string {
	encoded := strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
	return strings.NewReplacer("%21", "!", "%27", "'", "%28", "(", "%29", ")", "%2A", "*").Replace(encoded)
}

var hints = map[string]string{
	"SignatureDoesNotMatch":        "Kiểm tra khóa truy cập, vùng và kiểu đường dẫn forcePathStyle.",
	"AuthorizationHeaderMalformed": "Kiểm tra vùng của bucket và cấu hình endpoint.",
	"PermanentRedirect":            "Endpoint hoặc vùng chưa đúng với bucket.",
	"AccessDenied":                 "Kiểm tra quyền truy cập bucket và đối tượng.",
	"InvalidAccessKeyId":           "Kiểm tra mã khóa truy cập của hồ sơ.",
	"NoSuchBucket":                 "Bucket không tồn tại hoặc bạn không có quyền truy cập.",
	"NoSuchKey":                    "Đối tượng không còn tồn tại.",
}
