package validate

import (
	"regexp"
	"s3browser/internal/apperr"
	"strings"
	"unicode/utf16"
)

var uuid = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func Length(s string) int { return len(utf16.Encode([]rune(s))) }
func Text(s, label string, max int, empty bool) error {
	if (!empty && s == "") || Length(s) > max || strings.ContainsRune(s, 0) {
		return apperr.New("InvalidArgument", label+" không hợp lệ hoặc vượt quá độ dài cho phép.")
	}
	return nil
}
func ID(s string) error {
	if !uuid.MatchString(s) {
		return apperr.New("InvalidArgument", "Mã định danh phải là UUID.")
	}
	return nil
}
func Bucket(s string) error { return Text(s, "Tên bucket", 255, false) }
func Key(s string) error    { return Text(s, "Tên đối tượng", 1024, false) }
func Prefix(s string) error {
	if err := Text(s, "Prefix thư mục", 1024, true); err != nil {
		return err
	}
	if s != "" && !strings.HasSuffix(s, "/") {
		return apperr.New("InvalidArgument", "Prefix thư mục phải kết thúc bằng dấu /.")
	}
	return nil
}
func Strings(values []string, max int, check func(string) error) error {
	if len(values) == 0 || len(values) > max {
		return apperr.New("InvalidArgument", "Danh sách rỗng hoặc có quá nhiều mục.")
	}
	for _, v := range values {
		if err := check(v); err != nil {
			return err
		}
	}
	return nil
}
func First(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
