package sizer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"s3browser/internal/apperr"
	"s3browser/internal/backend"
	"s3browser/internal/profiles"
	"s3browser/internal/s3svc"
	"s3browser/internal/sizer"
	"s3browser/internal/testutil"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body == nil {
		r.Body = http.NoBody
	}
	return f(r)
}

func xmlResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/xml"}, "Etag": []string{"mã-kiểm-thử"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

type eventCollector struct {
	mu     sync.Mutex
	events []sizer.Event
	times  []time.Time
}

func (c *eventCollector) record(ev sizer.Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.times = append(c.times, time.Now())
	c.mu.Unlock()
}

func (c *eventCollector) list() []sizer.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]sizer.Event, len(c.events))
	copy(out, c.events)
	return out
}

func (c *eventCollector) reset() {
	c.mu.Lock()
	c.events = nil
	c.times = nil
	c.mu.Unlock()
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Quá thời gian chờ điều kiện kiểm thử")
}

func setupBackend(t *testing.T, handler roundTrip) (*backend.Backend, profiles.Profile, *eventCollector) {
	t.Helper()
	store, rawProfile, _, _ := testutil.Fixture(t)
	collector := &eventCollector{}
	b := backend.New(store, nil, collector.record)
	saved, err := b.Save(rawProfile)
	if err != nil {
		t.Fatalf("Không lưu được hồ sơ: %v", err)
	}
	setTransport(t, b.S3, saved.ID, handler)
	t.Cleanup(b.Close)
	return b, saved, collector
}

func setTransport(t *testing.T, svc *s3svc.Service, id string, handler roundTrip) {
	t.Helper()
	c, err := svc.Client(id)
	if err != nil {
		t.Fatalf("Không lấy được client: %v", err)
	}
	if tr, ok := c.Options().HTTPClient.(*http.Client); ok {
		tr.Transport = handler
	} else {
		t.Fatal("HTTPClient không phải *http.Client")
	}
}

func TestCộngDồnNhiềuTrangVàPhânTrangContinuationToken(t *testing.T) {
	var calls atomic.Int32
	b, p, collector := setupBackend(t, func(r *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		q := r.URL.Query()
		if q.Get("delimiter") != "" || q.Get("max-keys") != "1000" || q.Get("prefix") != "thư mục/" {
			t.Errorf("Tham số truy vấn không đúng: %v", r.URL.RawQuery)
		}
		if n == 1 {
			if q.Get("continuation-token") != "" {
				t.Error("Trang đầu không được có continuation-token")
			}
			return xmlResponse(r, 200, `<ListBucketResult>`+
				`<Contents><Key>thư mục/</Key><Size>0</Size></Contents>`+
				`<Contents><Key>thư mục/tệp-1.bin</Key><Size>120</Size></Contents>`+
				`<Contents><Key>thư mục/tệp-2.bin</Key><Size>380</Size></Contents>`+
				`<NextContinuationToken>trang-hai</NextContinuationToken>`+
				`</ListBucketResult>`), nil
		}
		if q.Get("continuation-token") != "trang-hai" {
			t.Errorf("Token phân trang sai: %q", q.Get("continuation-token"))
		}
		return xmlResponse(r, 200, `<ListBucketResult>`+
			`<Contents><Key>thư mục/con/</Key><Size>0</Size></Contents>`+
			`<Contents><Key>thư mục/con/tệp-3.bin</Key><Size>500</Size></Contents>`+
			`</ListBucketResult>`), nil
	})

	ok, err := b.Sizer.Start(p.ID, "kho", "thư mục/", false)
	if err != nil || !ok {
		t.Fatalf("Không bắt đầu tính được: ok=%v err=%v", ok, err)
	}
	waitUntil(t, func() bool {
		evs := collector.list()
		return len(evs) > 0 && evs[len(evs)-1].State == "done"
	})

	evs := collector.list()
	if len(evs) < 3 || evs[0].State != "queued" || evs[0].Bytes != 0 || evs[0].Objects != 0 {
		t.Fatalf("Sự kiện đầu không phải queued hợp lệ: %+v", evs)
	}
	hasRunning := false
	for _, ev := range evs {
		if ev.State == "running" {
			hasRunning = true
		}
	}
	if !hasRunning {
		t.Fatal("Thiếu sự kiện running")
	}
	last := evs[len(evs)-1]
	if last.State != "done" || last.Bytes != 1000 || last.Objects != 5 || last.ProfileID != p.ID || last.Bucket != "kho" || last.Prefix != "thư mục/" || last.Message != "" {
		t.Fatalf("Kết quả cuối không đúng: %+v", last)
	}
	if calls.Load() != 2 {
		t.Fatalf("Số lần gọi trang = %d, cần 2", calls.Load())
	}

	raw, err := json.Marshal(last)
	if err != nil {
		t.Fatal("Không mã hóa được JSON sự kiện")
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 7 {
		t.Fatalf("JSON sự kiện thiếu trường theo hợp đồng: %s", string(raw))
	}
}

func TestGiớiHạnHaiViệcĐồngThờiVàKhôngChạyTrùng(t *testing.T) {
	var active, maximum, totalCalls atomic.Int32
	gate := make(chan struct{})
	var orderMu sync.Mutex
	var order []string

	b, p, collector := setupBackend(t, func(r *http.Request) (*http.Response, error) {
		totalCalls.Add(1)
		prefix := r.URL.Query().Get("prefix")
		orderMu.Lock()
		order = append(order, prefix)
		orderMu.Unlock()

		n := active.Add(1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)

		<-gate
		return xmlResponse(r, 200, `<ListBucketResult><Contents><Key>`+prefix+`tệp</Key><Size>10</Size></Contents></ListBucketResult>`), nil
	})

	prefixes := []string{"một/", "hai/", "ba/", "bốn/", "năm/"}
	for _, pfx := range prefixes {
		if ok, err := b.Sizer.Start(p.ID, "kho", pfx, false); err != nil || !ok {
			t.Fatalf("Không xếp hàng được %s: %v", pfx, err)
		}
	}
	// Gọi lại khóa đang chạy và khóa đang xếp hàng: không được tạo việc trùng.
	if ok, err := b.Sizer.Start(p.ID, "kho", "một/", false); err != nil || !ok {
		t.Fatal("Gọi lại khóa đang chạy thất bại")
	}
	if ok, err := b.Sizer.Start(p.ID, "kho", "bốn/", true); err != nil || !ok {
		t.Fatal("Gọi lại khóa đang xếp hàng thất bại")
	}

	waitUntil(t, func() bool { return active.Load() == 2 })
	time.Sleep(20 * time.Millisecond)
	if active.Load() != 2 || maximum.Load() != 2 || totalCalls.Load() != 2 {
		t.Fatalf("Giới hạn 2 việc đồng thời không đúng: active=%d max=%d calls=%d", active.Load(), maximum.Load(), totalCalls.Load())
	}

	// Giải phóng từng suất để kiểm chứng các việc xếp hàng chạy đúng thứ tự gọi: ba/ -> bốn/ -> năm/.
	for i := 3; i <= len(prefixes); i++ {
		gate <- struct{}{}
		waitUntil(t, func() bool { return totalCalls.Load() == int32(i) })
		if active.Load() > 2 || maximum.Load() > 2 {
			t.Fatalf("Vượt quá 2 việc đồng thời ở bước %d", i)
		}
	}
	close(gate)

	waitUntil(t, func() bool {
		doneCount := 0
		for _, ev := range collector.list() {
			if ev.State == "done" {
				doneCount++
			}
		}
		return doneCount == len(prefixes)
	})

	if maximum.Load() != 2 {
		t.Fatalf("Số việc đồng thời tối đa = %d, cần 2", maximum.Load())
	}
	if totalCalls.Load() != int32(len(prefixes)) {
		t.Fatalf("Tổng số lần chạy = %d, cần %d (đã chạy trùng)", totalCalls.Load(), len(prefixes))
	}
	orderMu.Lock()
	tailOrder := strings.Join(order[2:], ",")
	orderMu.Unlock()
	if tailOrder != "ba/,bốn/,năm/" {
		t.Fatalf("Thứ tự lấy khỏi hàng đợi = %s, cần ba/,bốn/,năm/", tailOrder)
	}
}

func TestHủyGiữaChừngVàHủyKhiĐangXếpHàng(t *testing.T) {
	page2Entered := make(chan struct{})
	blockOther := make(chan struct{})

	b, p, collector := setupBackend(t, func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		prefix := q.Get("prefix")
		if prefix == "đang-chạy/" {
			if q.Get("continuation-token") == "" {
				return xmlResponse(r, 200, `<ListBucketResult>`+
					`<Contents><Key>đang-chạy/1</Key><Size>100</Size></Contents>`+
					`<Contents><Key>đang-chạy/2</Key><Size>150</Size></Contents>`+
					`<NextContinuationToken>tiếp</NextContinuationToken>`+
					`</ListBucketResult>`), nil
			}
			close(page2Entered)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if prefix == "giữ-suất/" {
			<-blockOther
			return xmlResponse(r, 200, `<ListBucketResult/>`), nil
		}
		t.Errorf("Khóa đã hủy khi xếp hàng vẫn bị gọi HTTP: %s", prefix)
		return xmlResponse(r, 200, `<ListBucketResult/>`), nil
	})

	_, _ = b.Sizer.Start(p.ID, "kho", "đang-chạy/", false)
	_, _ = b.Sizer.Start(p.ID, "kho", "giữ-suất/", false)
	_, _ = b.Sizer.Start(p.ID, "kho", "xếp-hàng/", false)

	<-page2Entered

	// Hủy khóa đang xếp hàng.
	ok, err := b.Sizer.Cancel(p.ID, "kho", "xếp-hàng/")
	if err != nil || !ok {
		t.Fatalf("Không hủy được khóa đang xếp hàng: ok=%v err=%v", ok, err)
	}
	// Hủy khóa đang chạy giữa chừng ở trang 2.
	ok, err = b.Sizer.Cancel(p.ID, "kho", "đang-chạy/")
	if err != nil || !ok {
		t.Fatalf("Không hủy được khóa đang chạy: ok=%v err=%v", ok, err)
	}
	// Gọi hủy lần nữa cho khóa đã hủy hoặc không tồn tại phải trả false, nil.
	if ok, err := b.Sizer.Cancel(p.ID, "kho", "đang-chạy/"); err != nil || ok {
		t.Fatalf("Hủy lặp lại phải trả false, nil: ok=%v err=%v", ok, err)
	}
	if ok, err := b.Sizer.Cancel(p.ID, "kho", "không-tồn-tại/"); err != nil || ok {
		t.Fatalf("Hủy khóa không có phải trả false, nil: ok=%v err=%v", ok, err)
	}

	close(blockOther)
	waitUntil(t, func() bool {
		for _, ev := range collector.list() {
			if ev.Prefix == "giữ-suất/" && ev.State == "done" {
				return true
			}
		}
		return false
	})

	var runningCancelled, queuedCancelled *sizer.Event
	for _, ev := range collector.list() {
		evCopy := ev
		if ev.Prefix == "đang-chạy/" && ev.State == "cancelled" {
			runningCancelled = &evCopy
		}
		if ev.Prefix == "xếp-hàng/" && ev.State == "cancelled" {
			queuedCancelled = &evCopy
		}
	}
	if runningCancelled == nil || runningCancelled.Bytes != 250 || runningCancelled.Objects != 2 {
		t.Fatalf("Sự kiện cancelled khi đang chạy không giữ đúng số cộng dồn: %+v", runningCancelled)
	}
	if queuedCancelled == nil || queuedCancelled.Bytes != 0 || queuedCancelled.Objects != 0 {
		t.Fatalf("Sự kiện cancelled khi đang xếp hàng không đúng: %+v", queuedCancelled)
	}
}

func TestCacheVàForce(t *testing.T) {
	var calls atomic.Int32
	b, p, collector := setupBackend(t, func(r *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		size := 40 * n
		return xmlResponse(r, 200, fmt.Sprintf(`<ListBucketResult><Contents><Key>a/tệp</Key><Size>%d</Size></Contents></ListBucketResult>`, size)), nil
	})

	// Lần 1: tính mới và lưu vào cache.
	_, _ = b.Sizer.Start(p.ID, "kho", "a/", false)
	waitUntil(t, func() bool {
		evs := collector.list()
		return len(evs) > 0 && evs[len(evs)-1].State == "done"
	})
	if calls.Load() != 1 {
		t.Fatalf("Lần 1 phải gọi HTTP 1 lần, thực tế %d", calls.Load())
	}

	// Lần 2: force=false phát ngay sự kiện done từ cache, không gọi HTTP.
	collector.reset()
	ok, err := b.Sizer.Start(p.ID, "kho", "a/", false)
	if err != nil || !ok {
		t.Fatal("Lấy từ cache thất bại")
	}
	evs := collector.list()
	if len(evs) != 1 || evs[0].State != "done" || evs[0].Bytes != 40 || evs[0].Objects != 1 || calls.Load() != 1 {
		t.Fatalf("Không trả ngay sự kiện done từ cache: calls=%d evs=%+v", calls.Load(), evs)
	}

	// Lần 3: force=true bỏ qua cache và tính lại.
	collector.reset()
	_, _ = b.Sizer.Start(p.ID, "kho", "a/", true)
	waitUntil(t, func() bool {
		list := collector.list()
		return len(list) > 0 && list[len(list)-1].State == "done"
	})
	evs = collector.list()
	last := evs[len(evs)-1]
	if calls.Load() != 2 || last.Bytes != 80 || last.Objects != 1 {
		t.Fatalf("Force không tính lại đúng: calls=%d last=%+v", calls.Load(), last)
	}

	// Lần 4: force=false lấy giá trị mới từ cache.
	collector.reset()
	_, _ = b.Sizer.Start(p.ID, "kho", "a/", false)
	evs = collector.list()
	if len(evs) != 1 || evs[0].State != "done" || evs[0].Bytes != 80 || calls.Load() != 2 {
		t.Fatalf("Cache sau force không được cập nhật: calls=%d evs=%+v", calls.Load(), evs)
	}
}

func TestVôHiệuCacheKhiGhiThànhCông(t *testing.T) {
	var listCalls atomic.Int32
	var failWrite atomic.Bool

	b, p, collector := setupBackend(t, func(r *http.Request) (*http.Response, error) {
		if failWrite.Load() && r.Method != http.MethodGet {
			return xmlResponse(r, 403, `<Error><Code>AccessDenied</Code><Message>Từ chối ghi</Message></Error>`), nil
		}
		if r.Method == http.MethodGet && r.URL.Query().Get("prefix") != "" || (r.Method == http.MethodGet && r.URL.RawQuery != "" && strings.Contains(r.URL.RawQuery, "max-keys")) {
			// Phân biệt Walk của DeletePrefix/Move/Copy với Walk của Sizer bằng tên bucket/prefix.
			if strings.HasPrefix(r.URL.Path, "/kho-đếm") || strings.HasPrefix(r.URL.Path, "/kho-khác") {
				listCalls.Add(1)
				return xmlResponse(r, 200, `<ListBucketResult><Contents><Key>a/tệp</Key><Size>10</Size></Contents></ListBucketResult>`), nil
			}
			return xmlResponse(r, 200, `<ListBucketResult><Contents><Key>nguồn/tệp</Key><Size>3</Size></Contents></ListBucketResult>`), nil
		}
		if r.Method == http.MethodHead {
			out := xmlResponse(r, 200, "")
			out.Header.Set("Content-Length", "3")
			return out, nil
		}
		if r.Method == http.MethodGet {
			out := xmlResponse(r, 200, "abc")
			out.Header.Set("Content-Length", "3")
			return out, nil
		}
		if r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "" {
			return xmlResponse(r, 200, `<CopyObjectResult><ETag>mã</ETag></CopyObjectResult>`), nil
		}
		if r.Method == http.MethodPost {
			return xmlResponse(r, 200, `<DeleteResult/>`), nil
		}
		return xmlResponse(r, 200, ""), nil
	})

	primeCache := func() {
		t.Helper()
		for _, target := range [][2]string{{"kho-đếm", ""}, {"kho-đếm", "a/"}, {"kho-khác", ""}} {
			collector.reset()
			_, _ = b.Sizer.Start(p.ID, target[0], target[1], false)
			waitUntil(t, func() bool {
				evs := collector.list()
				return len(evs) > 0 && evs[len(evs)-1].State == "done"
			})
		}
	}

	tempDir := testutil.Temporary(t)
	localPath := filepath.Join(tempDir, "tải-lên.txt")
	if err := os.WriteFile(localPath, []byte("abc"), 0600); err != nil {
		t.Fatal("Không tạo được tệp tạm để tải lên")
	}

	actions := []struct {
		name string
		run  func() error
	}{
		{"Delete", func() error {
			_, err := b.S3.Delete(context.Background(), p.ID, "kho-đếm", []string{"x"})
			return err
		}},
		{"DeletePrefix", func() error {
			_, err := b.S3.DeletePrefix(context.Background(), p.ID, "kho-đếm", "nguồn/")
			return err
		}},
		{"CreateFolder", func() error {
			_, err := b.S3.CreateFolder(context.Background(), p.ID, "kho-đếm", "mới/")
			return err
		}},
		{"Move", func() error { _, err := b.S3.Move(context.Background(), p.ID, "kho-đếm", "x", "y"); return err }},
		{"Upload", func() error {
			jobs, err := b.Transfers.Upload(p.ID, "kho-đếm", "a/", []string{localPath})
			if err != nil {
				return err
			}
			waitUntil(t, func() bool {
				for _, j := range b.Transfers.List() {
					if j.ID == jobs[0].ID && j.Status == "completed" {
						return true
					}
				}
				return false
			})
			return nil
		}},
		{"Copy", func() error {
			jobs, err := b.Transfers.Copy(p.ID, "kho-nguồn", []string{"nguồn/tệp"}, p.ID, "kho-đếm", "đích/")
			if err != nil {
				return err
			}
			waitUntil(t, func() bool {
				for _, j := range b.Transfers.List() {
					if j.ID == jobs[0].ID && j.Status == "completed" {
						return true
					}
				}
				return false
			})
			return nil
		}},
	}

	for _, act := range actions {
		t.Run(act.name, func(t *testing.T) {
			primeCache()
			before := listCalls.Load()
			// Kiểm tra đã ở trong cache.
			_, _ = b.Sizer.Start(p.ID, "kho-đếm", "", false)
			_, _ = b.Sizer.Start(p.ID, "kho-đếm", "a/", false)
			if listCalls.Load() != before {
				t.Fatal("Dữ liệu chưa được lấy từ cache trước khi ghi")
			}

			if err := act.run(); err != nil {
				t.Fatalf("Thao tác %s lỗi: %v", act.name, err)
			}

			// Bucket khác không bị mất cache.
			beforeOther := listCalls.Load()
			_, _ = b.Sizer.Start(p.ID, "kho-khác", "", false)
			if listCalls.Load() != beforeOther {
				t.Fatal("Thao tác ghi đã xóa nhầm cache của bucket khác")
			}

			// Cả gốc bucket và prefix con của kho-đếm đều phải tính lại.
			for _, pfx := range []string{"", "a/"} {
				collector.reset()
				prev := listCalls.Load()
				_, _ = b.Sizer.Start(p.ID, "kho-đếm", pfx, false)
				waitUntil(t, func() bool {
					evs := collector.list()
					return len(evs) > 0 && evs[len(evs)-1].State == "done"
				})
				if listCalls.Load() != prev+1 {
					t.Fatalf("Cache của (%s, %q) không được vô hiệu sau %s", "kho-đếm", pfx, act.name)
				}
			}
		})
	}

	// Khi thao tác ghi thất bại thì không xóa cache.
	primeCache()
	failWrite.Store(true)
	_, _ = b.S3.CreateFolder(context.Background(), p.ID, "kho-đếm", "lỗi/")
	failWrite.Store(false)
	before := listCalls.Load()
	_, _ = b.Sizer.Start(p.ID, "kho-đếm", "", false)
	if listCalls.Load() != before {
		t.Fatal("Thao tác ghi thất bại vẫn xóa cache")
	}
}

func TestHủyVàXóaCacheKhiHồSơSửaHoặcXóa(t *testing.T) {
	var listCalls atomic.Int32
	inPage2 := make(chan struct{}, 2)

	handler := roundTrip(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if q.Get("prefix") == "chậm/" {
			if q.Get("continuation-token") == "" {
				return xmlResponse(r, 200, `<ListBucketResult><Contents><Key>chậm/1</Key><Size>77</Size></Contents><NextContinuationToken>tiếp</NextContinuationToken></ListBucketResult>`), nil
			}
			inPage2 <- struct{}{}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		listCalls.Add(1)
		return xmlResponse(r, 200, `<ListBucketResult><Contents><Key>nhanh/1</Key><Size>15</Size></Contents></ListBucketResult>`), nil
	})

	b, p1, collector := setupBackend(t, handler)
	p2, err := b.Save(profiles.Profile{Name: "Hồ sơ 2", Endpoint: "https://s3.example.invalid", AccessKeyID: testutil.Random(), SecretAccessKey: testutil.Random()})
	if err != nil {
		t.Fatal("Không tạo được hồ sơ 2")
	}
	setTransport(t, b.S3, p2.ID, handler)

	// Đưa cả p1 và p2 vào cache.
	for _, id := range []string{p1.ID, p2.ID} {
		collector.reset()
		_, _ = b.Sizer.Start(id, "kho", "nhanh/", false)
		waitUntil(t, func() bool {
			evs := collector.list()
			return len(evs) > 0 && evs[len(evs)-1].State == "done"
		})
	}

	// Bắt đầu việc chạy lâu trên p1 rồi sửa hồ sơ p1 (không được báo ProfileBusy).
	collector.reset()
	_, _ = b.Sizer.Start(p1.ID, "kho", "chậm/", false)
	<-inPage2

	p1.Name = "Kho đã đổi tên"
	if _, err := b.Save(p1); err != nil {
		t.Fatalf("Sửa hồ sơ bị chặn hoặc lỗi khi đang tính dung lượng: %v", err)
	}
	setTransport(t, b.S3, p1.ID, handler)

	// Kiểm tra việc đang chạy của p1 đã phát sự kiện cancelled với đúng số cộng dồn.
	var cancelled *sizer.Event
	for _, ev := range collector.list() {
		evCopy := ev
		if ev.ProfileID == p1.ID && ev.Prefix == "chậm/" && ev.State == "cancelled" {
			cancelled = &evCopy
		}
	}
	if cancelled == nil || cancelled.Bytes != 77 || cancelled.Objects != 1 {
		t.Fatalf("Sửa hồ sơ không hủy đúng việc đang chạy: %+v", cancelled)
	}

	// Cache của p2 vẫn còn, cache của p1 đã bị xóa.
	before := listCalls.Load()
	_, _ = b.Sizer.Start(p2.ID, "kho", "nhanh/", false)
	if listCalls.Load() != before {
		t.Fatal("Sửa p1 đã xóa nhầm cache của p2")
	}
	collector.reset()
	_, _ = b.Sizer.Start(p1.ID, "kho", "nhanh/", false)
	waitUntil(t, func() bool {
		evs := collector.list()
		return len(evs) > 0 && evs[len(evs)-1].State == "done"
	})
	if listCalls.Load() != before+1 {
		t.Fatal("Sửa p1 không xóa cache của p1")
	}

	// Bắt đầu việc chạy lâu trên p1 rồi xóa hồ sơ p1.
	collector.reset()
	_, _ = b.Sizer.Start(p1.ID, "kho", "chậm/", false)
	<-inPage2
	if ok, err := b.Remove(p1.ID); err != nil || !ok {
		t.Fatalf("Xóa hồ sơ thất bại khi đang tính dung lượng: ok=%v err=%v", ok, err)
	}
	cancelled = nil
	for _, ev := range collector.list() {
		evCopy := ev
		if ev.ProfileID == p1.ID && ev.Prefix == "chậm/" && ev.State == "cancelled" {
			cancelled = &evCopy
		}
	}
	if cancelled == nil || cancelled.Bytes != 77 {
		t.Fatalf("Xóa hồ sơ không hủy việc đang chạy: %+v", cancelled)
	}
}

func TestShutdownKhôngRòGoroutine(t *testing.T) {
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	baseGoroutines := runtime.NumGoroutine()

	store, rawProfile, _, _ := testutil.Fixture(t)
	collector := &eventCollector{}
	b := backend.New(store, nil, collector.record)
	p, err := b.Save(rawProfile)
	if err != nil {
		t.Fatal("Không lưu được hồ sơ")
	}

	var started atomic.Int32
	setTransport(t, b.S3, p.ID, func(r *http.Request) (*http.Response, error) {
		started.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	for _, pfx := range []string{"một/", "hai/", "ba/", "bốn/"} {
		_, _ = b.Sizer.Start(p.ID, "kho", pfx, false)
	}
	waitUntil(t, func() bool { return started.Load() == 2 })

	b.Close()

	cancelledCount := 0
	for _, ev := range collector.list() {
		if ev.State == "cancelled" {
			cancelledCount++
		}
	}
	if cancelledCount != 4 {
		t.Fatalf("Số sự kiện cancelled khi đóng = %d, cần 4", cancelledCount)
	}

	if _, err := b.Sizer.Start(p.ID, "kho", "năm/", false); err == nil {
		t.Fatal("Đã cho phép Start sau khi Close")
	}

	waitUntil(t, func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= baseGoroutines+1
	})
}

func TestSựKiệnRunningKhôngDàyHơn300ms(t *testing.T) {
	var page atomic.Int32
	b, p, collector := setupBackend(t, func(r *http.Request) (*http.Response, error) {
		n := page.Add(1)
		if n > 1 {
			time.Sleep(120 * time.Millisecond)
		}
		token := ""
		if n < 5 {
			token = fmt.Sprintf("<NextContinuationToken>trang-%d</NextContinuationToken>", n+1)
		}
		var body strings.Builder
		body.WriteString("<ListBucketResult>" + token)
		for i := 0; i < 10; i++ {
			fmt.Fprintf(&body, "<Contents><Key>a/%d-%d</Key><Size>10</Size></Contents>", n, i)
		}
		body.WriteString("</ListBucketResult>")
		return xmlResponse(r, 200, body.String()), nil
	})

	_, _ = b.Sizer.Start(p.ID, "kho", "a/", false)
	waitUntil(t, func() bool {
		evs := collector.list()
		return len(evs) > 0 && evs[len(evs)-1].State == "done"
	})

	collector.mu.Lock()
	var runningTimes []time.Time
	for i, ev := range collector.events {
		if ev.State == "running" {
			runningTimes = append(runningTimes, collector.times[i])
		}
	}
	collector.mu.Unlock()

	if len(runningTimes) < 2 {
		t.Fatalf("Cần ít nhất 2 sự kiện running để kiểm chứng giãn cách, nhận được %d", len(runningTimes))
	}
	for i := 1; i < len(runningTimes); i++ {
		gap := runningTimes[i].Sub(runningTimes[i-1])
		if gap < 300*time.Millisecond {
			t.Fatalf("Hai sự kiện running cách nhau %v (< 300ms)", gap)
		}
	}
}

func TestLỗiĐượcLọcSecretVàKhôngCache(t *testing.T) {
	store, rawProfile, _, _ := testutil.Fixture(t)
	secret := testutil.Random() + " /+đặc-biệt"
	accessKey := testutil.Random()
	rawProfile.AccessKeyID = accessKey
	rawProfile.SecretAccessKey = secret

	collector := &eventCollector{}
	b := backend.New(store, nil, collector.record)
	defer b.Close()
	p, err := b.Save(rawProfile)
	if err != nil {
		t.Fatal("Không lưu được hồ sơ")
	}

	var calls atomic.Int32
	setTransport(t, b.S3, p.ID, func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return xmlResponse(r, 403, `<Error><Code>SignatureDoesNotMatch</Code><Message>Sai chữ ký với `+secret+` và `+accessKey+`</Message></Error>`), nil
		}
		return xmlResponse(r, 200, `<ListBucketResult><Contents><Key>a/tệp</Key><Size>25</Size></Contents></ListBucketResult>`), nil
	})

	_, _ = b.Sizer.Start(p.ID, "kho", "a/", false)
	waitUntil(t, func() bool {
		evs := collector.list()
		return len(evs) > 0 && evs[len(evs)-1].State == "error"
	})

	evs := collector.list()
	errEv := evs[len(evs)-1]
	if strings.Contains(errEv.Message, secret) || strings.Contains(errEv.Message, accessKey) || !strings.Contains(errEv.Message, "[đã ẩn]") {
		t.Fatalf("Thông điệp lỗi chưa lọc sạch khóa nhạy cảm: %q", errEv.Message)
	}
	if !strings.Contains(errEv.Message, "forcePathStyle") {
		t.Fatalf("Thông điệp lỗi thiếu gợi ý tiếng Việt: %q", errEv.Message)
	}

	// Lỗi không được cache: gọi lại với force=false phải chạy lại và thành công.
	collector.reset()
	_, _ = b.Sizer.Start(p.ID, "kho", "a/", false)
	waitUntil(t, func() bool {
		list := collector.list()
		return len(list) > 0 && list[len(list)-1].State == "done"
	})
	if calls.Load() != 2 {
		t.Fatalf("Lỗi đã bị cache, số lần gọi = %d", calls.Load())
	}
	if got := collector.list(); got[len(got)-1].Bytes != 25 {
		t.Fatalf("Kết quả chạy lại không đúng: %+v", got[len(got)-1])
	}

	// Kiểm chứng lọc lỗi khi Filter được truyền nil.
	s := sizer.New(b.S3, nil, nil)
	defer s.Close()
	_ = apperr.New("Test", "Kiểm thử")
}
