package webserver_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"s3browser/frontend"
	"s3browser/internal/apperr"
	"s3browser/internal/backend"
	"s3browser/internal/profiles"
	"s3browser/internal/service"
	"s3browser/internal/sizer"
	"s3browser/internal/testutil"
	"s3browser/internal/transfers"
	"s3browser/internal/webserver"
)

const testToken = "token-kiem-thu-du-16-ky-tu"

type testEnv struct {
	server  *webserver.Server
	backend *backend.Backend
	profile profiles.Profile
	tempDir string
}

func newTestEnv(t *testing.T, s3Handler http.HandlerFunc) *testEnv {
	t.Helper()
	store, p, _, _ := testutil.Fixture(t)
	if s3Handler != nil {
		s3Srv := httptest.NewServer(s3Handler)
		t.Cleanup(s3Srv.Close)
		p.Endpoint = s3Srv.URL
	}
	var webSrv atomic.Pointer[webserver.Server]
	b := backend.New(store, func(job transfers.Job) {
		if s := webSrv.Load(); s != nil {
			s.PublishTransfer(job)
		}
	}, func(ev sizer.Event) {
		if s := webSrv.Load(); s != nil {
			s.PublishSize(ev)
		}
	})
	t.Cleanup(b.Close)
	saved, err := b.Save(p)
	if err != nil {
		t.Fatalf("Không tạo được hồ sơ kiểm thử: %v", err)
	}
	tempDir := testutil.Temporary(t)
	srv, err := webserver.New(webserver.Config{
		Service:      service.New(b),
		Assets:       frontend.FS,
		Token:        testToken,
		ListenAddr:   "127.0.0.1:8080",
		TempDir:      tempDir,
		LoginDelay:   -1,
		PingInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Không khởi tạo được máy chủ web: %v", err)
	}
	webSrv.Store(srv)
	t.Cleanup(srv.Close)
	return &testEnv{server: srv, backend: b, profile: saved, tempDir: tempDir}
}

func authedRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, "http://127.0.0.1:8080"+target, body)
	req.AddCookie(&http.Cookie{Name: "s3b_session", Value: testToken})
	return req
}

func checkErrorJSON(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("Mã HTTP không đúng: nhận %d, cần %d (thân: %s)", rec.Code, wantStatus, rec.Body.String())
	}
	var errObj apperr.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &errObj); err != nil {
		t.Fatalf("Phản hồi lỗi không phải JSON hợp lệ: %v (%s)", err, rec.Body.String())
	}
	if errObj.Code != wantCode || errObj.Message == "" {
		t.Fatalf("Lỗi JSON không đúng: nhận %+v, cần mã %s", errObj, wantCode)
	}
}

func TestThayĐổiIndexHTMLVàTừChốiKhiThiếuChuỗi(t *testing.T) {
	env := newTestEnv(t, nil)
	for _, p := range []string{"/", "/index.html"} {
		rec := httptest.NewRecorder()
		env.server.ServeHTTP(rec, authedRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s trả mã %d", p, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, `src="./api-shim.js"`) || !strings.Contains(body, `src="./api-web.js"`) {
			t.Fatalf("GET %s chưa đổi sang ./api-web.js", p)
		}
		if strings.Contains(body, "form-action 'none'") || !strings.Contains(body, "form-action 'self'") {
			t.Fatalf("GET %s chưa đổi CSP form-action sang 'self'", p)
		}
		if strings.Contains(body, "frame-src 'none'") || !strings.Contains(body, "frame-src 'self'") {
			t.Fatalf("GET %s chưa đổi CSP frame-src sang 'self'", p)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "same-origin" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("Thiếu tiêu đề bảo mật bắt buộc")
		}
	}

	// /api-shim.js phải trả 404.
	recShim := httptest.NewRecorder()
	env.server.ServeHTTP(recShim, authedRequest(http.MethodGet, "/api-shim.js", nil))
	checkErrorJSON(t, recShim, http.StatusNotFound, "NotFound")

	// Tệp tĩnh khác phục vụ nguyên trạng.
	recCSS := httptest.NewRecorder()
	env.server.ServeHTTP(recCSS, authedRequest(http.MethodGet, "/styles.css", nil))
	if recCSS.Code != http.StatusOK || !strings.Contains(recCSS.Header().Get("Content-Type"), "text/css") || recCSS.Body.Len() == 0 {
		t.Fatalf("Không phục vụ đúng styles.css: code=%d, ct=%s", recCSS.Code, recCSS.Header().Get("Content-Type"))
	}

	// Nếu index.html thiếu chuỗi cần thay thì New phải từ chối khởi động.
	for _, badHTML := range []string{
		`<html><head><meta http-equiv="Content-Security-Policy" content="form-action 'none'; frame-src 'none'"></head></html>`,
		`<html><head><script src="./api-shim.js"></script></head></html>`,
		`<html><head><script src="./api-shim.js"></script><meta http-equiv="Content-Security-Policy" content="form-action 'none'"></head></html>`,
	} {
		_, err := webserver.New(webserver.Config{
			Service: service.New(env.backend),
			Assets:  fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(badHTML)}},
			Token:   testToken,
			TempDir: testutil.Temporary(t),
		})
		if err == nil {
			t.Fatal("Máy chủ vẫn khởi động khi index.html thiếu chuỗi bắt buộc")
		}
	}
}

func TestĐăngNhậpCookieVàChặnAPIChưaXácThực(t *testing.T) {
	env := newTestEnv(t, nil)

	// Chưa có cookie: đường dẫn thường trả trang đăng nhập kèm CSP chặt.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `action="/login"`) || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("Trang đăng nhập không đúng: code=%d, csp=%s", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}

	// GET /?token=sai -> hiện trang đăng nhập kèm lỗi, không đặt cookie.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/?token=sai-token", nil)
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), `role="alert"`) {
		t.Fatalf("GET /?token=sai không xử lý đúng: code=%d", rec.Code)
	}

	// GET /?token=đúng -> đặt cookie HttpOnly, SameSite=Strict và 302 về /.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/?token="+testToken, nil)
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("GET /?token=đúng phải 302 về /, nhận %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "s3b_session" || cookies[0].Value != testToken || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("Cookie phiên không đúng hợp đồng: %+v", cookies)
	}

	// POST /login sai -> chờ 500ms và hiện lại trang đăng nhập kèm lỗi.
	srvDelay, err := webserver.New(webserver.Config{
		Service:    service.New(env.backend),
		Assets:     frontend.FS,
		Token:      testToken,
		ListenAddr: "127.0.0.1:8080",
		TempDir:    testutil.Temporary(t),
		LoginDelay: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srvDelay.Close()
	start := time.Now()
	rec = httptest.NewRecorder()
	form := url.Values{"token": {"sai"}}
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srvDelay.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || time.Since(start) < 450*time.Millisecond || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("POST /login sai không đúng: code=%d, elapsed=%v", rec.Code, time.Since(start))
	}

	// POST /login đúng -> đặt cookie và 303 về /.
	rec = httptest.NewRecorder()
	form = url.Values{"token": {testToken}}
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("POST /login đúng phải 303 về / và đặt cookie, nhận %d", rec.Code)
	}

	// Mọi đường dẫn /api/ chưa xác thực trả 401 JSON.
	for _, apiPath := range []string{"/api/rpc/ProfilesList", "/api/events", "/api/upload", "/api/download"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+apiPath, nil)
		env.server.ServeHTTP(rec, req)
		checkErrorJSON(t, rec, http.StatusUnauthorized, "Unauthorized")
	}
}

func TestChốngDNSRebindingVàCSRF(t *testing.T) {
	env := newTestEnv(t, nil)

	for _, badHost := range []string{"evil.com", "evil.com:8080", "127.0.0.1:9999", "127.0.0.1", "localhost"} {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/", nil)
		req.Host = badHost
		env.server.ServeHTTP(rec, req)
		checkErrorJSON(t, rec, http.StatusForbidden, "Forbidden")
	}

	for _, goodHost := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/", nil)
		req.Host = goodHost
		env.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Host hợp lệ %s bị chặn với mã %d", goodHost, rec.Code)
		}
	}

	// POST có Origin sai -> 403.
	for _, badOrigin := range []string{"http://evil.com", "http://127.0.0.1:9999", "https://127.0.0.1:8080", "null"} {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/rpc/ProfilesList", strings.NewReader("[]"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", badOrigin)
		env.server.ServeHTTP(rec, req)
		checkErrorJSON(t, rec, http.StatusForbidden, "Forbidden")
	}

	// POST có Origin khớp scheme://Host -> 200.
	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/rpc/ProfilesList", strings.NewReader("[]"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Origin hợp lệ bị từ chối: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRPCThànhCôngLỗiNghiệpVụVàChặnPhươngThứcDesktop(t *testing.T) {
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchBucket</Code><Message>Bucket không tồn tại</Message></Error>`)
	})

	// RPC thành công: ProfilesList.
	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/rpc/ProfilesList", strings.NewReader("[]"))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ProfilesList lỗi: %d %s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Result []profiles.Profile `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil || len(listResp.Result) != 1 || listResp.Result[0].ID != env.profile.ID {
		t.Fatalf("Kết quả ProfilesList không đúng: %+v, err=%v", listResp, err)
	}

	// Lỗi nghiệp vụ: trả 400 với thân chính là apperr.Error (không bọc).
	rec = httptest.NewRecorder()
	body, _ := json.Marshal([]any{env.profile.ID, "kho-không-tồn-tại", "", ""})
	req = authedRequest(http.MethodPost, "/api/rpc/S3List", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	env.server.ServeHTTP(rec, req)
	checkErrorJSON(t, rec, http.StatusBadRequest, "NoSuchBucket")

	// Lỗi nghiệp vụ kiểm tra tham số (InvalidBucketName).
	rec = httptest.NewRecorder()
	body, _ = json.Marshal([]any{env.profile.ID, "Tên_Sai"})
	req = authedRequest(http.MethodPost, "/api/rpc/S3CreateBucket", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	env.server.ServeHTTP(rec, req)
	checkErrorJSON(t, rec, http.StatusBadRequest, "InvalidBucketName")

	// MethodNotFound và các phương thức chỉ dành cho bản desktop (ClipboardWriteText, DialogsUpload, DialogsDownloadDirectory).
	for _, method := range []string{"KhôngTồnTại", "ClipboardWriteText", "DialogsUpload", "DialogsDownloadDirectory"} {
		rec = httptest.NewRecorder()
		req = authedRequest(http.MethodPost, "/api/rpc/"+method, strings.NewReader("[]"))
		req.Header.Set("Content-Type", "application/json")
		env.server.ServeHTTP(rec, req)
		checkErrorJSON(t, rec, http.StatusNotFound, "MethodNotFound")
	}

	// InvalidArguments: sai số lượng, sai kiểu, không phải mảng JSON, truyền null.
	for _, badBody := range []string{`["thừa"]`, `{}`, `null`, `not-json`} {
		rec = httptest.NewRecorder()
		req = authedRequest(http.MethodPost, "/api/rpc/ProfilesList", strings.NewReader(badBody))
		req.Header.Set("Content-Type", "application/json")
		env.server.ServeHTTP(rec, req)
		checkErrorJSON(t, rec, http.StatusBadRequest, "InvalidArguments")
	}
	for _, badArgs := range []string{`[123]`, `[null]`, `[]`} {
		rec = httptest.NewRecorder()
		req = authedRequest(http.MethodPost, "/api/rpc/S3ListBuckets", strings.NewReader(badArgs))
		req.Header.Set("Content-Type", "application/json")
		env.server.ServeHTTP(rec, req)
		checkErrorJSON(t, rec, http.StatusBadRequest, "InvalidArguments")
	}

	// Sai Content-Type.
	rec = httptest.NewRecorder()
	req = authedRequest(http.MethodPost, "/api/rpc/ProfilesList", strings.NewReader("[]"))
	req.Header.Set("Content-Type", "text/plain")
	env.server.ServeHTTP(rec, req)
	checkErrorJSON(t, rec, http.StatusBadRequest, "InvalidContentType")
}

func TestSSENhậnSựKiệnTransfersUpdate(t *testing.T) {
	env := newTestEnv(t, nil)
	ts := httptest.NewServer(env.server)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "s3b_session", Value: testToken})
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Không kết nối được SSE: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("Tiêu đề SSE không đúng: status=%d, ct=%s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	total := int64(42)
	wantJob := transfers.Job{
		ID:          "job-1",
		Type:        "upload",
		Name:        "báo-cáo.txt",
		Status:      "running",
		Transferred: 21,
		Total:       &total,
	}
	env.server.PublishTransfer(wantJob)

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	var gotEvent, gotData string
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("Đọc luồng SSE lỗi: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "event: ") {
			gotEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			gotData = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if gotEvent != "transfers:update" {
		t.Fatalf("Tên sự kiện SSE không đúng: %q", gotEvent)
	}
	var gotJob transfers.Job
	if err := json.Unmarshal([]byte(gotData), &gotJob); err != nil || gotJob.ID != wantJob.ID || gotJob.Name != wantJob.Name || gotJob.Transferred != 21 {
		t.Fatalf("Dữ liệu SSE không khớp: %+v, err=%v", gotJob, err)
	}

	// Kiểm tra nhận sự kiện size:update và dòng ping.
	env.server.PublishSize(sizer.Event{ProfileID: env.profile.ID, Bucket: "kho", Prefix: "a/", State: "done", Bytes: 100, Objects: 2})
	var gotSizeEvent, gotSizeData string
	var sawPing bool
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (gotSizeData == "" || !sawPing) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("Đọc luồng SSE lỗi: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == ": ping" {
			sawPing = true
		} else if strings.HasPrefix(line, "event: ") {
			gotSizeEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") && gotSizeEvent == "size:update" {
			gotSizeData = strings.TrimPrefix(line, "data: ")
		}
	}
	if gotSizeEvent != "size:update" || !strings.Contains(gotSizeData, `"bytes":100`) || !sawPing {
		t.Fatalf("Thiếu size:update hoặc ping trên SSE: event=%q data=%q ping=%v", gotSizeEvent, gotSizeData, sawPing)
	}
}

func buildMultipartUpload(t *testing.T, items [][2]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, item := range items {
		pw, err := mw.CreateFormField("path")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(pw, item[0])
		fw, err := mw.CreateFormFile("file", "ignored.bin")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(fw, item[1])
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func waitQueueFinished(t *testing.T, b *backend.Backend) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs := b.Transfers.List()
		allDone := len(jobs) > 0
		for _, j := range jobs {
			if j.Status == "queued" || j.Status == "running" || j.Status == "cancelling" {
				allDone = false
				break
			}
		}
		if allDone {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Quá thời gian chờ hàng đợi hoàn tất")
}

func TestUploadTệpThưMụcLồngNhauTừChốiPathĐộcHạiVàDọnTạm(t *testing.T) {
	var (
		mu       sync.Mutex
		uploaded = map[string]string{}
	)
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			key := strings.TrimPrefix(r.URL.Path, "/kho/")
			mu.Lock()
			uploaded[key] = string(b)
			mu.Unlock()
			w.Header().Set("ETag", `"etag-1"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Từ chối các đường dẫn độc hại và dọn sạch thư mục tạm của yêu cầu lỗi.
	badPaths := []string{
		"",
		"/tuyệt-đối.txt",
		"../ngoài.txt",
		"thư-mục/../../ngoài.txt",
		"./tệp.txt",
		"thư-mục/./tệp.txt",
		`thư-mục\tệp.txt`,
		"thư-mục//tệp.txt",
		"thư-mục/",
		"tệp\x00.txt",
	}
	for _, bad := range badPaths {
		body, ct := buildMultipartUpload(t, [][2]string{
			{"hợp-lệ.txt", "dữ liệu tạm"},
			{bad, "độc hại"},
		})
		rec := httptest.NewRecorder()
		target := fmt.Sprintf("/api/upload?profile=%s&bucket=kho&prefix=", url.QueryEscape(env.profile.ID))
		req := authedRequest(http.MethodPost, target, body)
		req.Header.Set("Content-Type", ct)
		env.server.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Đường dẫn độc hại %q không bị chặn: code=%d", bad, rec.Code)
		}
	}
	if jobs := env.backend.Transfers.List(); len(jobs) != 0 {
		t.Fatalf("Yêu cầu upload lỗi đã tạo job: %+v", jobs)
	}
	entries, err := os.ReadDir(env.tempDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Thư mục tạm chưa được dọn sau khi upload lỗi: %v", entries)
	}

	// Upload hợp lệ gồm tệp đơn và thư mục lồng nhau.
	body, ct := buildMultipartUpload(t, [][2]string{
		{"lẻ.txt", "nội dung lẻ"},
		{"thư-mục/con/a.txt", "nội dung a"},
		{"thư-mục/b.txt", "nội dung b"},
	})
	rec := httptest.NewRecorder()
	target := fmt.Sprintf("/api/upload?profile=%s&bucket=kho&prefix=%s", url.QueryEscape(env.profile.ID), url.QueryEscape("đích/"))
	req := authedRequest(http.MethodPost, target, body)
	req.Header.Set("Content-Type", ct)
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Upload hợp lệ thất bại: %d %s", rec.Code, rec.Body.String())
	}
	var upResp struct {
		Result []transfers.Job `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &upResp); err != nil || len(upResp.Result) != 2 {
		t.Fatalf("Phải tạo đúng 2 job cho 2 phần tử gốc, nhận: %+v, err=%v", upResp.Result, err)
	}
	if upResp.Result[0].Name != "lẻ.txt" || upResp.Result[1].Name != "thư-mục" {
		t.Fatalf("Tên job không đúng phần tử gốc: %+v", upResp.Result)
	}

	waitQueueFinished(t, env.backend)

	mu.Lock()
	if uploaded["đích/lẻ.txt"] != "nội dung lẻ" || uploaded["đích/thư-mục/con/a.txt"] != "nội dung a" || uploaded["đích/thư-mục/b.txt"] != "nội dung b" {
		t.Fatalf("Dữ liệu S3 nhận được không khớp: %+v", uploaded)
	}
	mu.Unlock()

	// Khi các job đã completed, thư mục tạm của từng phần tử gốc phải được dọn sạch.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ = os.ReadDir(env.tempDir)
		if len(entries) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	entries, _ = os.ReadDir(env.tempDir)
	if len(entries) != 0 {
		t.Fatalf("Thư mục tạm chưa được dọn sau khi job completed: %d mục còn lại", len(entries))
	}
}

func TestUploadGiữTạmKhiLỗiĐểThửLạiRồiDọnKhiThànhCông(t *testing.T) {
	var failUpload atomic.Bool
	failUpload.Store(true)
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if failUpload.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code><Message>Lỗi tạm thời</Message></Error>`)
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"etag-ok"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	body, ct := buildMultipartUpload(t, [][2]string{{"thử-lại.txt", "dữ liệu thử lại"}})
	rec := httptest.NewRecorder()
	target := fmt.Sprintf("/api/upload?profile=%s&bucket=kho&prefix=", url.QueryEscape(env.profile.ID))
	req := authedRequest(http.MethodPost, target, body)
	req.Header.Set("Content-Type", ct)
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Upload xếp hàng lỗi: %d", rec.Code)
	}
	var upResp struct {
		Result []transfers.Job `json:"result"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &upResp)
	jobID := upResp.Result[0].ID

	waitQueueFinished(t, env.backend)
	entries, _ := os.ReadDir(env.tempDir)
	if len(entries) == 0 {
		t.Fatal("Job failed nhưng thư mục lưu tạm đã bị xóa, không thể thử lại")
	}

	failUpload.Store(false)
	retryBody, _ := json.Marshal([]any{jobID})
	recRetry := httptest.NewRecorder()
	reqRetry := authedRequest(http.MethodPost, "/api/rpc/TransfersRetry", bytes.NewReader(retryBody))
	reqRetry.Header.Set("Content-Type", "application/json")
	env.server.ServeHTTP(recRetry, reqRetry)
	if recRetry.Code != http.StatusOK {
		t.Fatalf("TransfersRetry qua RPC thất bại: %d %s", recRetry.Code, recRetry.Body.String())
	}

	waitQueueFinished(t, env.backend)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ = os.ReadDir(env.tempDir)
		if len(entries) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	entries, _ = os.ReadDir(env.tempDir)
	if len(entries) != 0 {
		t.Fatal("Thư mục lưu tạm không được dọn sau khi thử lại thành công")
	}
}

func TestDownloadTệpĐơnVàZipCóThưMục(t *testing.T) {
	env := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/kho" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<ListBucketResult>
				<Contents><Key>thư-mục/con/a.txt</Key><Size>9</Size></Contents>
				<Contents><Key>thư-mục/b.txt</Key><Size>9</Size></Contents>
			</ListBucketResult>`)
			return
		}
		switch r.URL.Path {
		case "/kho/báo-cáo.txt":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "dữ liệu 1")
		case "/kho/thư-mục/con/a.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "nội dungA")
		case "/kho/thư-mục/b.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "nội dungB")
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>Không có khóa</Message></Error>`)
		}
	})

	// GET tệp đơn.
	rec := httptest.NewRecorder()
	target := fmt.Sprintf("/api/download?profile=%s&bucket=kho&key=%s", url.QueryEscape(env.profile.ID), url.QueryEscape("báo-cáo.txt"))
	env.server.ServeHTTP(rec, authedRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "dữ liệu 1" {
		t.Fatalf("GET download tệp đơn lỗi: code=%d, body=%q", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment;") || !strings.Contains(cd, "filename*=UTF-8''b%C3%A1o-c%C3%A1o.txt") {
		t.Fatalf("Content-Disposition không đúng: %q", cd)
	}
	if rec.Header().Get("Content-Length") != "13" || !strings.Contains(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("Content-Type/Content-Length không đúng: ct=%q, cl=%q", rec.Header().Get("Content-Type"), rec.Header().Get("Content-Length"))
	}

	// GET khóa không tồn tại trả 404 JSON.
	rec = httptest.NewRecorder()
	targetMissing := fmt.Sprintf("/api/download?profile=%s&bucket=kho&key=khong-co.txt", url.QueryEscape(env.profile.ID))
	env.server.ServeHTTP(rec, authedRequest(http.MethodGet, targetMissing, nil))
	checkErrorJSON(t, rec, http.StatusNotFound, "NoSuchKey")

	// POST tải xuống nhiều khóa + thư mục (bỏ qua khóa trùng) trả zip.Store.
	keysJSON, _ := json.Marshal([]string{"thư-mục/", "thư-mục/con/a.txt", "báo-cáo.txt"})
	form := url.Values{
		"profile": {env.profile.ID},
		"bucket":  {"kho"},
		"keys":    {string(keysJSON)},
	}
	rec = httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/download", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("POST download zip lỗi: code=%d, ct=%q, body=%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), `filename="kho.zip"`) {
		t.Fatalf("Tên zip nhiều mục phải là kho.zip, nhận: %q", rec.Header().Get("Content-Disposition"))
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("Không đọc được luồng zip trả về: %v", err)
	}
	if len(zr.File) != 3 {
		t.Fatalf("Zip phải có đúng 3 mục (đã loại trùng), nhận %d", len(zr.File))
	}
	gotFiles := map[string]string{}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Fatalf("Mục zip %s không dùng zip.Store: %d", f.Name, f.Method)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		gotFiles[f.Name] = string(b)
	}
	if gotFiles["thư-mục/con/a.txt"] != "nội dungA" || gotFiles["thư-mục/b.txt"] != "nội dungB" || gotFiles["báo-cáo.txt"] != "dữ liệu 1" {
		t.Fatalf("Nội dung các mục trong zip không khớp: %+v", gotFiles)
	}

	// POST một thư mục duy nhất -> tên zip là <tên thư mục>.zip.
	singleFolderJSON, _ := json.Marshal([]string{"thư-mục/"})
	form.Set("keys", string(singleFolderJSON))
	rec = httptest.NewRecorder()
	req = authedRequest(http.MethodPost, "/api/download", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	env.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Disposition"), "filename*=UTF-8''th%C6%B0-m%E1%BB%A5c.zip") {
		t.Fatalf("Tên zip một thư mục không đúng: %q", rec.Header().Get("Content-Disposition"))
	}

	// Từ chối khóa chứa .. hoặc bắt đầu bằng /.
	for _, badKey := range []string{"/tuyệt-đối", "../ngoài", "a/../b"} {
		badJSON, _ := json.Marshal([]string{badKey})
		form.Set("keys", string(badJSON))
		rec = httptest.NewRecorder()
		req = authedRequest(http.MethodPost, "/api/download", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		env.server.ServeHTTP(rec, req)
		checkErrorJSON(t, rec, http.StatusBadRequest, "UnsafePath")
	}
}
