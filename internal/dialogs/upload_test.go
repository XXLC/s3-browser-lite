package dialogs

import "testing"

func TestChọnNhiềuThưMụcBỏTrùng(t *testing.T) {
	choices := []string{`D:\Một`, `d:\một`, `D:\Hai`}
	calls := 0
	paths, err := selectDirectories(func() (string, error) { path := choices[calls]; calls++; return path, nil }, func(int) (bool, error) { return calls < 3, nil })
	if err != nil || len(paths) != 2 {
		t.Fatal("Không gom được nhiều thư mục hoặc chưa bỏ trùng")
	}
}
func TestHủyChọnThưMụcTrảDanhSáchRỗng(t *testing.T) {
	paths, err := selectDirectories(func() (string, error) { return "", nil }, func(int) (bool, error) { t.Fatal("Không được hỏi tiếp sau khi hủy"); return false, nil })
	if err != nil || paths == nil || len(paths) != 0 {
		t.Fatal("Hủy hộp thoại trả sai kết quả")
	}
}
