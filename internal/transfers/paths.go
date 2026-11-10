package transfers

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"s3browser/internal/apperr"
	"s3browser/internal/validate"
	"strings"
)

var forbidden = regexp.MustCompile(`[\\\x00-\x1f<>:"|?*]`)
var reserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\.|$)`)

func unsafePath() error {
	return apperr.New("UnsafePath", "Tên đối tượng hoặc đường dẫn đích không an toàn trên Windows.")
}
func SafeDownloadPath(root, key string) (string, error) {
	if key == "" || validate.Length(key) > 4096 || forbidden.MatchString(key) || strings.HasPrefix(key, "/") {
		return "", unsafePath()
	}
	parts := strings.Split(strings.TrimSuffix(key, "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || reserved.MatchString(part) {
			return "", unsafePath()
		}
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", unsafePath()
	}
	target := filepath.Join(append([]string{base}, parts...)...)
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == "." || !inside(relative) {
		return "", unsafePath()
	}
	return target, nil
}
func inside(relative string) bool {
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
func EnsureSafeDirectory(root, directory string) error {
	base, err := filepath.Abs(root)
	if err != nil {
		return unsafePath()
	}
	target, err := filepath.Abs(directory)
	if err != nil {
		return unsafePath()
	}
	relative, err := filepath.Rel(base, target)
	if err != nil || !inside(relative) {
		return unsafePath()
	}
	check := func(path string) error {
		stat, err := os.Lstat(path)
		if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 || reparse(stat) {
			return unsafePath()
		}
		return nil
	}
	if err = check(base); err != nil {
		return err
	}
	current := base
	if relative == "." {
		return nil
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err = os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return unsafePath()
		}
		if err = check(current); err != nil {
			return err
		}
	}
	return nil
}

type localFile struct {
	path, key string
	size      int64
	directory bool
}

func localFiles(ctx context.Context, path, relative string, files *[]localFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || reparse(info) {
		return unsafePath()
	}
	if info.IsDir() {
		*files = append(*files, localFile{path, relative + "/", 0, true})
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = localFiles(ctx, filepath.Join(path, entry.Name()), relative+"/"+entry.Name(), files); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return apperr.New("InvalidFile", "Đường dẫn tải lên không phải tệp hoặc thư mục thông thường.")
	}
	*files = append(*files, localFile{path, relative, info.Size(), false})
	return nil
}
