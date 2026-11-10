package dialogs

import (
	"context"
	"fmt"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func Upload(ctx context.Context, directory bool) ([]string, error) {
	if !directory {
		paths, err := runtime.OpenMultipleFilesDialog(ctx, runtime.OpenDialogOptions{Title: "Chọn tệp tải lên"})
		if paths == nil && err == nil {
			paths = []string{}
		}
		return paths, err
	}
	// Wails chỉ chọn một thư mục mỗi lần; gom nhiều lần chọn vào cùng danh sách.
	return selectDirectories(func() (string, error) {
		return runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: "Chọn thư mục tải lên"})
	}, func(count int) (bool, error) {
		answer, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
			Type: runtime.QuestionDialog, Title: "Chọn thư mục tải lên",
			Message: fmt.Sprintf("Đã chọn %d thư mục. Bạn có muốn chọn thêm thư mục không?", count),
			Buttons: []string{"Yes", "No"}, DefaultButton: "No", CancelButton: "No",
		})
		return answer == "Yes", err
	})
}

func selectDirectories(open func() (string, error), more func(int) (bool, error)) ([]string, error) {
	paths := []string{}
	seen := map[string]bool{}
	for len(paths) < 1000 {
		path, err := open()
		if err != nil {
			return nil, err
		}
		if path == "" {
			return paths, nil
		}
		normalized := strings.ToLower(path)
		if !seen[normalized] {
			paths = append(paths, path)
			seen[normalized] = true
		}
		if len(paths) == 1000 {
			break
		}
		again, err := more(len(paths))
		if err != nil {
			return nil, err
		}
		if !again {
			break
		}
	}
	return paths, nil
}
