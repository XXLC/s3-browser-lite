package transfers

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"s3browser/internal/apperr"
	"s3browser/internal/s3svc"
	"s3browser/internal/testutil"
)

func until(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Quá thời gian chờ tác vụ kiểm thử")
}
func finished(q *Queue) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active != 0 {
		return false
	}
	for _, job := range q.jobs {
		if job.view.Status == "queued" || job.view.Status == "running" || job.view.Status == "cancelling" {
			return false
		}
	}
	return true
}
func expectCode(t *testing.T, err error, want string) {
	t.Helper()
	var own *apperr.Error
	if !errors.As(err, &own) || own.Code != want {
		t.Fatalf("Không nhận được mã lỗi %s", want)
	}
}
func TestChặnĐườngDẫnWindows(t *testing.T) {
	root := testutil.Temporary(t)
	for _, key := range []string{"../ngoài", "a/../../ngoài", "/tệp", "C:/tệp", `a\..\tệp`, "a//b", "a/./b", "CON", "aux.txt", "LPT1/tệp", "com9.log", "COM¹.txt", "tệp.", "tệp ", "a:b", "a?b", "a\x00b", "//máy/chia-sẻ", ""} {
		_, err := SafeDownloadPath(root, key)
		expectCode(t, err, "UnsafePath")
	}
	for _, key := range []string{"thư mục/tệp.txt", "thư mục/"} {
		got, err := SafeDownloadPath(root, key)
		want := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(key, "/")))
		if err != nil || got != want {
			t.Fatal("Đường dẫn hợp lệ bị chặn")
		}
	}
}
func TestThưMụcAnToànChặnTệpVàThoátGốc(t *testing.T) {
	root := testutil.Temporary(t)
	target := filepath.Join(root, "một", "hai")
	if err := EnsureSafeDirectory(root, target); err != nil {
		t.Fatal("Không tạo được thư mục an toàn")
	}
	_ = os.WriteFile(filepath.Join(root, "chặn"), []byte("Nội dung kiểm thử"), 0600)
	expectCode(t, EnsureSafeDirectory(root, filepath.Join(root, "chặn", "con")), "UnsafePath")
	expectCode(t, EnsureSafeDirectory(root, filepath.Dir(root)), "UnsafePath")
}
func TestĐồngThờiMặcĐịnhBaTácVụ(t *testing.T) {
	q := NewQueue(&apperr.Filter{}, nil)
	defer q.Close()
	var running, maximum atomic.Int32
	for i := 0; i < 9; i++ {
		_, err := q.Add("upload", "Tệp kiểm thử", []string{"hồ-sơ"}, func(ctx context.Context, progress Progress) error {
			n := running.Add(1)
			for {
				old := maximum.Load()
				if n <= old || maximum.CompareAndSwap(old, n) {
					break
				}
			}
			progress(5, 10)
			time.Sleep(15 * time.Millisecond)
			progress(10, 10)
			running.Add(-1)
			return nil
		})
		if err != nil {
			t.Fatal("Không thêm được tác vụ")
		}
	}
	until(t, func() bool { return finished(q) })
	if maximum.Load() != 3 {
		t.Fatal("Giới hạn đồng thời không đúng")
	}
	for _, job := range q.List() {
		if job.Status != "completed" || job.Transferred != 10 || job.Total == nil || *job.Total != 10 || job.Speed != 0 {
			t.Fatal("Tiến độ cuối không đúng")
		}
	}
}
func TestHủyĐangChạyGiữSuấtVàThửLại(t *testing.T) {
	q := NewQueue(&apperr.Filter{}, nil)
	defer q.Close()
	_, _ = q.SetConcurrency(1)
	started := make(chan struct{})
	release := make(chan struct{})
	var attempts, secondRuns atomic.Int32
	first, _ := q.Add("download", "Tệp một", []string{"hồ-sơ"}, func(ctx context.Context, _ Progress) error {
		if attempts.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			<-release
		}
		return nil
	})
	<-started
	second, _ := q.Add("download", "Tệp hai", nil, func(context.Context, Progress) error { secondRuns.Add(1); return nil })
	_, _ = q.Cancel(second.ID)
	_, _ = q.Cancel(first.ID)
	if q.List()[0].Status != "cancelling" || !q.UsesProfile("hồ-sơ") {
		t.Fatal("Hủy đang chạy đã trả suất quá sớm")
	}
	if _, err := q.Retry(first.ID); err == nil {
		t.Fatal("Đã thử lại tác vụ đang hủy")
	}
	close(release)
	until(t, func() bool { return finished(q) })
	if secondRuns.Load() != 0 || q.List()[0].Status != "cancelled" {
		t.Fatal("Hủy không đúng")
	}
	_, _ = q.Retry(first.ID)
	_, _ = q.Retry(second.ID)
	until(t, func() bool { return finished(q) })
	if attempts.Load() != 2 || secondRuns.Load() != 1 || q.UsesProfile("hồ-sơ") {
		t.Fatal("Thử lại không đúng")
	}
}
func TestLỗiThửLạiĐóngVàThayGiớiHạn(t *testing.T) {
	q := NewQueue(&apperr.Filter{}, nil)
	var attempts atomic.Int32
	job, _ := q.Add("copy", "Tệp", nil, func(context.Context, Progress) error {
		if attempts.Add(1) == 1 {
			return apperr.New("AccessDenied", "Máy chủ từ chối kiểm thử.")
		}
		return nil
	})
	until(t, func() bool { return finished(q) })
	if q.List()[0].Error.Code != "AccessDenied" {
		t.Fatal("Mất mã lỗi tác vụ")
	}
	_, _ = q.Retry(job.ID)
	until(t, func() bool { return finished(q) })
	if _, err := q.Retry(job.ID); err == nil {
		t.Fatal("Thử lại tác vụ đã xong")
	}
	for _, n := range []int{0, 11, -1} {
		_, err := q.SetConcurrency(n)
		expectCode(t, err, "InvalidConcurrency")
	}
	if n, err := q.SetConcurrency(4); n != 4 || err != nil {
		t.Fatal("Không đặt được giới hạn")
	}
	q.Close()
	_, err := q.Add("upload", "Tệp", nil, nil)
	expectCode(t, err, "QueueClosed")
	_, err = q.Cancel("không-có")
	expectCode(t, err, "TransferNotFound")
}

type fakeService struct {
	walk   func(context.Context, string, string, string, func(s3svc.Object) error) error
	head   func(context.Context, string, string, string) (s3svc.HeadResult, error)
	get    func(context.Context, string, string, string) (s3svc.Download, error)
	upload func(context.Context, string, string, s3svc.Upload, func(int64)) error
}

func (f *fakeService) Walk(ctx context.Context, id, bucket, prefix string, visit func(s3svc.Object) error) error {
	if f.walk == nil {
		return nil
	}
	return f.walk(ctx, id, bucket, prefix, visit)
}
func (f *fakeService) Head(ctx context.Context, id, bucket, key string) (s3svc.HeadResult, error) {
	if f.head == nil {
		return s3svc.HeadResult{Size: 3}, nil
	}
	return f.head(ctx, id, bucket, key)
}
func (f *fakeService) Get(ctx context.Context, id, bucket, key string) (s3svc.Download, error) {
	if f.get == nil {
		return s3svc.Download{Body: io.NopCloser(strings.NewReader("abc")), Size: 3, ContentType: "text/plain", Metadata: map[string]string{"mô_tả": "bản thử"}}, nil
	}
	return f.get(ctx, id, bucket, key)
}
func (f *fakeService) Upload(ctx context.Context, id, bucket string, input s3svc.Upload, progress func(int64)) error {
	if f.upload == nil {
		return apperr.New("UnexpectedUpload", "Không được tải lên trong kiểm thử này.")
	}
	return f.upload(ctx, id, bucket, input, progress)
}
func transfer(t *testing.T, service *fakeService) *Transfers {
	t.Helper()
	q := New(service, &apperr.Filter{}, nil)
	t.Cleanup(q.Close)
	return q
}
func terminal(t *testing.T, q *Transfers, index int, status, code string) {
	t.Helper()
	until(t, func() bool { return finished(q.Queue) })
	jobs := q.List()
	if len(jobs) <= index || jobs[index].Status != status {
		t.Fatalf("Trạng thái tác vụ không đúng, cần %s", status)
	}
	if code != "" && (jobs[index].Error == nil || jobs[index].Error.Code != code) {
		t.Fatalf("Mã lỗi tác vụ không đúng, cần %s", code)
	}
}
func TestTảiLênĐệQuyGồmThưMụcRỗng(t *testing.T) {
	root := testutil.Temporary(t)
	folder := filepath.Join(root, "nguồn")
	_ = os.MkdirAll(filepath.Join(folder, "rỗng"), 0700)
	_ = os.WriteFile(filepath.Join(folder, "tệp.txt"), []byte("Dữ liệu"), 0600)
	var mu sync.Mutex
	received := map[string]string{}
	service := &fakeService{upload: func(ctx context.Context, id, bucket string, in s3svc.Upload, progress func(int64)) error {
		b, err := io.ReadAll(in.Body)
		if err != nil {
			return err
		}
		mu.Lock()
		received[in.Key] = string(b)
		mu.Unlock()
		progress(int64(len(b)))
		return nil
	}}
	q := transfer(t, service)
	_, _ = q.Upload("hồ-sơ", "kho", "đích/", []string{folder})
	terminal(t, q, 0, "completed", "")
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 || received["đích/nguồn/tệp.txt"] != "Dữ liệu" {
		t.Fatal("Không tải đủ cây thư mục")
	}
	if _, ok := received["đích/nguồn/rỗng/"]; !ok {
		t.Fatal("Mất thư mục rỗng")
	}
}
func walkFiles(files ...s3svc.Object) func(context.Context, string, string, string, func(s3svc.Object) error) error {
	return func(ctx context.Context, id, bucket, prefix string, visit func(s3svc.Object) error) error {
		for _, file := range files {
			if err := visit(file); err != nil {
				return err
			}
		}
		return nil
	}
}
func TestTảiXuốngĐệQuyBỏTrùngKhôngGhiĐè(t *testing.T) {
	root := testutil.Temporary(t)
	service := &fakeService{walk: walkFiles(s3svc.Object{Key: "thư mục/"}, s3svc.Object{Key: "thư mục/tệp", Size: 3})}
	q := transfer(t, service)
	_, _ = q.Download("hồ-sơ", "kho", []string{"thư mục/", "thư mục/tệp"}, root)
	terminal(t, q, 0, "completed", "")
	file := filepath.Join(root, "thư mục", "tệp")
	b, _ := os.ReadFile(file)
	if string(b) != "abc" {
		t.Fatal("Nội dung tải xuống không đúng")
	}
	_, _ = q.Download("hồ-sơ", "kho", []string{"thư mục/tệp"}, root)
	terminal(t, q, 1, "failed", "FileExists")
	b, _ = os.ReadFile(file)
	if string(b) != "abc" {
		t.Fatal("Tệp cũ đã bị ghi đè")
	}
}
func TestThửLạiTảiXuốngBỏTệpXongVàDọnTạm(t *testing.T) {
	root := testutil.Temporary(t)
	var fail atomic.Bool
	fail.Store(true)
	service := &fakeService{get: func(ctx context.Context, id, bucket, key string) (s3svc.Download, error) {
		if key == "hai" && fail.Load() {
			return s3svc.Download{}, apperr.New("TestError", "Lỗi kiểm thử.")
		}
		return s3svc.Download{Body: io.NopCloser(strings.NewReader("abc")), Size: 3}, nil
	}}
	q := transfer(t, service)
	jobs, _ := q.Download("hồ-sơ", "kho", []string{"một", "hai"}, root)
	terminal(t, q, 0, "failed", "TestError")
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "một" {
		t.Fatal("Có tệp tạm hoặc mất tệp đã xong")
	}
	fail.Store(false)
	_, _ = q.Retry(jobs[0].ID)
	terminal(t, q, 0, "completed", "")
	entries, _ = os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatal("Thử lại không giữ đủ kết quả")
	}
}
func TestHủyTảiXuốngDọnTạmVàĐóngLuồng(t *testing.T) {
	root := testutil.Temporary(t)
	reader, writer := io.Pipe()
	defer writer.Close()
	service := &fakeService{get: func(context.Context, string, string, string) (s3svc.Download, error) {
		return s3svc.Download{Body: reader, Size: 1024}, nil
	}}
	q := transfer(t, service)
	jobs, _ := q.Download("hồ-sơ", "kho", []string{"tệp"}, root)
	until(t, func() bool { entries, _ := os.ReadDir(root); return len(entries) > 0 })
	_, _ = q.Cancel(jobs[0].ID)
	terminal(t, q, 0, "cancelled", "")
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("Hủy còn tệp tạm hoặc tệp đích")
	}
	if _, err := writer.Write([]byte("Dữ liệu")); err == nil {
		t.Fatal("Luồng chưa được đóng")
	}
}
func TestTảiXuốngChặnĐườngDẫnVàTrùngTênTrướcGet(t *testing.T) {
	for _, files := range [][]s3svc.Object{{{Key: "a/../../ngoài", Size: 1}}, {{Key: "a/Tệp", Size: 1}, {Key: "a/tệp", Size: 1}}} {
		var gets atomic.Int32
		service := &fakeService{walk: walkFiles(files...), get: func(context.Context, string, string, string) (s3svc.Download, error) {
			gets.Add(1)
			return s3svc.Download{}, nil
		}}
		q := transfer(t, service)
		_, _ = q.Download("hồ-sơ", "kho", []string{"a/"}, testutil.Temporary(t))
		want := "UnsafePath"
		if len(files) > 1 {
			want = "PathCollision"
		}
		terminal(t, q, 0, "failed", want)
		if gets.Load() != 0 {
			t.Fatal("Đã gọi Get trước khi xác thực toàn bộ đích")
		}
	}
}
func TestLỗiLuồngTảiXuốngDọnTạm(t *testing.T) {
	root := testutil.Temporary(t)
	reader, writer := io.Pipe()
	service := &fakeService{get: func(context.Context, string, string, string) (s3svc.Download, error) {
		return s3svc.Download{Body: reader, Size: 3}, nil
	}}
	q := transfer(t, service)
	_, _ = q.Download("hồ-sơ", "kho", []string{"tệp"}, root)
	go func() {
		_, _ = writer.Write([]byte("a"))
		_ = writer.CloseWithError(apperr.New("StreamError", "Luồng kiểm thử bị ngắt."))
	}()
	terminal(t, q, 0, "failed", "StreamError")
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("Lỗi luồng còn tệp tạm")
	}
}
func TestSaoChépEndpointGiữMetadata(t *testing.T) {
	var uploaded atomic.Bool
	service := &fakeService{walk: walkFiles(s3svc.Object{Key: "cha/thư mục/tệp", Size: 3}), upload: func(ctx context.Context, id, bucket string, in s3svc.Upload, progress func(int64)) error {
		b, err := io.ReadAll(in.Body)
		if err != nil {
			return err
		}
		if id != "đích" || bucket != "kho-đích" || in.Key != "đích/thư mục/tệp" || string(b) != "abc" || in.ContentType != "text/plain" || in.Metadata["mô_tả"] != "bản thử" {
			return apperr.New("TestError", "Nội dung sao chép không đúng.")
		}
		uploaded.Store(true)
		progress(3)
		return nil
	}}
	q := transfer(t, service)
	_, _ = q.Copy("nguồn", "kho-nguồn", []string{"cha/thư mục/"}, "đích", "kho-đích", "đích/")
	terminal(t, q, 0, "completed", "")
	if !uploaded.Load() {
		t.Fatal("Chưa tải luồng lên endpoint đích")
	}
}
func TestSaoChépChặnTrùngNguồnVàĐích(t *testing.T) {
	q := transfer(t, &fakeService{})
	_, _ = q.Copy("nguồn", "kho", []string{"a"}, "nguồn", "kho", "")
	_, _ = q.Copy("nguồn", "kho", []string{"a/tệp", "b/tệp"}, "đích", "kho", "")
	terminal(t, q, 0, "failed", "InvalidCopy")
	terminal(t, q, 1, "failed", "CopyCollision")
}
func TestSaoChépChặnGhiĐèNguồnKhác(t *testing.T) {
	q := transfer(t, &fakeService{walk: walkFiles(s3svc.Object{Key: "a/x", Size: 1})})
	_, _ = q.Copy("nguồn", "kho", []string{"a/", "b/a/x"}, "nguồn", "kho", "b/")
	terminal(t, q, 0, "failed", "InvalidCopy")
}
func TestTránhGhiĐèTệpXuấtHiệnTrongKhiTải(t *testing.T) {
	root := testutil.Temporary(t)
	target := filepath.Join(root, "tệp")
	service := &fakeService{get: func(context.Context, string, string, string) (s3svc.Download, error) {
		_ = os.WriteFile(target, []byte("Tệp xuất hiện đồng thời"), 0600)
		return s3svc.Download{Body: io.NopCloser(strings.NewReader("abc")), Size: 3}, nil
	}}
	q := transfer(t, service)
	_, _ = q.Download("hồ-sơ", "kho", []string{"tệp"}, root)
	terminal(t, q, 0, "failed", "FileExists")
	b, _ := os.ReadFile(target)
	if string(b) != "Tệp xuất hiện đồng thời" {
		t.Fatal("Đã ghi đè tệp được tạo đồng thời")
	}
}
