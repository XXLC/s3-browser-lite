package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"s3browser/internal/backend"
	"s3browser/internal/profiles"
	"s3browser/internal/service"
	"s3browser/internal/testutil"
	"s3browser/internal/transfers"
)

func fixture(t *testing.T) (*App, profiles.Profile) {
	t.Helper()
	store, p, _, _ := testutil.Fixture(t)
	b := backend.New(store, nil)
	saved, err := b.Save(p)
	if err != nil {
		t.Fatal("Không tạo được hồ sơ kiểm thử")
	}
	a := &App{Service: service.New(b)}
	t.Cleanup(b.Close)
	return a, saved
}
func errorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Thiếu lỗi %s", want)
	}
	var result map[string]string
	if json.Unmarshal([]byte(err.Error()), &result) != nil || len(result) != 2 || result["code"] != want || result["message"] == "" {
		t.Fatalf("Lỗi không đúng hợp đồng JSON, cần %s", want)
	}
}
func TestAppKiểmTraThamSốVàTrảLỗiJSON(t *testing.T) {
	a, p := fixture(t)
	cases := []struct {
		name string
		run  func() error
	}{
		{"Mã hồ sơ", func() error { _, err := a.ProfilesRemove("../ngoài"); return err }},
		{"Hồ sơ rỗng", func() error { _, err := a.ProfilesSave(profiles.Profile{}); return err }},
		{"Token dài", func() error { _, err := a.S3List(p.ID, "kho", "", strings.Repeat("a", 16385)); return err }},
		{"Prefix sai", func() error { _, err := a.S3List(p.ID, "kho", "sai", ""); return err }},
		{"Danh sách rỗng", func() error { _, err := a.S3Delete(p.ID, "kho", nil); return err }},
		{"Khóa rỗng", func() error { _, err := a.S3Head(p.ID, "kho", ""); return err }},
		{"Xóa gốc", func() error { _, err := a.S3DeletePrefix(p.ID, "kho", ""); return err }},
		{"Đường dẫn rỗng", func() error { _, err := a.TransfersUpload(p.ID, "kho", "", []string{""}); return err }},
		{"Bucket đích", func() error { _, err := a.TransfersCopy(p.ID, "kho", []string{"a"}, p.ID, "", ""); return err }},
		{"Bảng tạm dài", func() error { _, err := a.ClipboardWriteText(strings.Repeat("a", 32769)); return err }},
		{"Prefix tính dung lượng sai", func() error { _, err := a.S3SizeStart(p.ID, "kho", "sai", false); return err }},
		{"Prefix hủy dung lượng sai", func() error { _, err := a.S3SizeCancel(p.ID, "kho", "sai"); return err }},
		{"Đọc policy mã hồ sơ sai", func() error { _, err := a.S3GetBucketPolicy("sai", "kho"); return err }},
		{"Đọc policy bucket rỗng", func() error { _, err := a.S3GetBucketPolicy(p.ID, ""); return err }},
		{"Ghi policy rỗng", func() error { _, err := a.S3PutBucketPolicy(p.ID, "kho", ""); return err }},
		{"Ghi policy quá dài", func() error { _, err := a.S3PutBucketPolicy(p.ID, "kho", strings.Repeat("a", 20481)); return err }},
		{"Xóa policy bucket rỗng", func() error { _, err := a.S3DeleteBucketPolicy(p.ID, ""); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := "InvalidArgument"
			if tc.name == "Hồ sơ rỗng" {
				want = "InvalidProfile"
			}
			errorCode(t, tc.run(), want)
		})
	}
	_, err := a.TransfersSetConcurrency(11)
	errorCode(t, err, "InvalidConcurrency")
	_, err = a.DialogsUpload(false)
	errorCode(t, err, "RuntimeUnavailable")
	_, err = a.DialogsDownloadDirectory()
	errorCode(t, err, "RuntimeUnavailable")
	_, err = a.ClipboardWriteText("Nội dung")
	errorCode(t, err, "RuntimeUnavailable")
}
func TestAppKhôngTrảSecretKhiLưuLiệtKêXuấtVàNhập(t *testing.T) {
	a, p := fixture(t)
	secret := testutil.Random()
	p.SecretAccessKey = secret
	saved, err := a.ProfilesSave(p)
	if err != nil {
		t.Fatal("Không sửa được hồ sơ")
	}
	list, _ := a.ProfilesList()
	exported, _ := a.ProfilesExport()
	duplicate, _ := a.ProfilesDuplicate(p.ID)
	raw, _ := json.Marshal(p)
	imported, err := a.ProfilesImport("[" + string(raw) + "]")
	if err != nil || imported[0].HasSecret {
		t.Fatal("Nhập chưa bỏ khóa")
	}
	for _, v := range []any{saved, list, exported, duplicate, imported} {
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), secret) || strings.Contains(string(b), "secretAccessKey") {
			t.Fatal("API đã trả khóa bí mật")
		}
	}
}
func TestAppChỉTảiXuốngThưMụcĐãChọn(t *testing.T) {
	a, p := fixture(t)
	directory := testutil.Temporary(t)
	_, err := a.TransfersDownload(p.ID, "kho", []string{"a"}, directory)
	errorCode(t, err, "ForbiddenPath")
	chosen, err := a.Backend.ChooseDirectory(directory)
	if err != nil {
		t.Fatal("Không cấp quyền thư mục kiểm thử")
	}
	if err = a.Backend.CheckDirectory(chosen); err != nil {
		t.Fatal("Không nhận thư mục đã chọn")
	}
	_, err = a.TransfersDownload(p.ID, "kho", []string{"a"}, filepath.Join(chosen, "con"))
	errorCode(t, err, "ForbiddenPath")
	if jobs, _ := a.TransfersList(); len(jobs) != 0 {
		t.Fatal("Đã tạo tác vụ ghi ngoài thư mục được cấp")
	}
}
func TestAppKhóaHồSơTrongThaoTácS3(t *testing.T) {
	a, p := fixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `<ListAllMyBucketsResult/>`)
	}))
	defer server.Close()
	original, err := a.Backend.Profiles.Get(p.ID)
	if err != nil {
		t.Fatal("Không lấy được hồ sơ giả lập")
	}
	original.Endpoint = server.URL
	p, err = a.ProfilesSave(original)
	if err != nil {
		t.Fatal("Không lưu được endpoint nội bộ")
	}
	result := make(chan error, 1)
	go func() { _, err := a.S3ListBuckets(p.ID); result <- err }()
	<-entered
	_, err = a.ProfilesSave(p)
	errorCode(t, err, "ProfileBusy")
	_, err = a.ProfilesRemove(p.ID)
	errorCode(t, err, "ProfileBusy")
	close(release)
	if err = <-result; err != nil {
		t.Fatal("Thao tác S3 nội bộ lỗi")
	}
	if _, err = a.ProfilesSave(p); err != nil {
		t.Fatal("Khóa hồ sơ chưa được thả")
	}
}
func TestAppKhóaHồSơTrongHàngĐợi(t *testing.T) {
	a, p := fixture(t)
	started := make(chan struct{})
	job, err := a.Backend.Transfers.Add("download", "Tệp", []string{p.ID}, func(ctx context.Context, _ transfers.Progress) error { close(started); <-ctx.Done(); return ctx.Err() })
	if err != nil {
		t.Fatal("Không tạo được tác vụ giả lập")
	}
	<-started
	_, err = a.ProfilesRemove(p.ID)
	errorCode(t, err, "ProfileBusy")
	_, err = a.ProfilesSave(p)
	errorCode(t, err, "ProfileBusy")
	if _, err = a.TransfersCancel(job.ID); err != nil {
		t.Fatal("Không hủy được tác vụ")
	}
}
func TestAppLọcKhóaTrongMãLỗiMáyChủ(t *testing.T) {
	a, p := fixture(t)
	original, _ := a.Backend.Profiles.Get(p.ID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>Máy chủ từ chối `+original.SecretAccessKey+` `+original.AccessKeyID+`</Message></Error>`)
	}))
	defer server.Close()
	original.Endpoint = server.URL
	_, _ = a.ProfilesSave(original)
	_, err := a.S3ListBuckets(p.ID)
	errorCode(t, err, "AccessDenied")
	if strings.Contains(err.Error(), original.SecretAccessKey) || strings.Contains(err.Error(), original.AccessKeyID) {
		t.Fatal("Lỗi App chứa khóa truy cập")
	}
}
func TestAppChỉBindĐúngHợpĐồng(t *testing.T) {
	typ := reflect.TypeOf(&App{})
	expected := strings.Fields("ProfilesList ProfilesSave ProfilesRemove ProfilesDuplicate ProfilesTestID ProfilesTestDraft ProfilesImport ProfilesExport S3ListBuckets S3CreateBucket S3GetBucketPolicy S3PutBucketPolicy S3DeleteBucketPolicy S3List S3Head S3Delete S3DeletePrefix S3CreateFolder S3Move S3Presign S3SizeStart S3SizeCancel TransfersUpload TransfersDownload TransfersCopy TransfersCancel TransfersRetry TransfersList TransfersSetConcurrency DialogsUpload DialogsDownloadDirectory ClipboardWriteText")
	if typ.NumMethod() != len(expected) {
		t.Fatal("Có phương thức công khai ngoài hợp đồng")
	}
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	for _, name := range expected {
		method, ok := typ.MethodByName(name)
		if !ok || method.Type.NumOut() != 2 || method.Type.Out(1) != errorType {
			t.Fatalf("Phương thức %s không đúng hợp đồng", name)
		}
	}
}
func TestAppBucketPolicyTừChốiThamSốSaiTrướcKhiGọiMạngVàThựcThi(t *testing.T) {
	a, p := fixture(t)
	hits := 0
	current := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.Method {
		case http.MethodGet:
			if current == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `<Error><Code>NoSuchBucketPolicy</Code><Message>The bucket policy does not exist</Message></Error>`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, current)
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			current = string(b)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			current = ""
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	original, _ := a.Backend.Profiles.Get(p.ID)
	original.Endpoint = server.URL
	p, err := a.ProfilesSave(original)
	if err != nil {
		t.Fatal("Không lưu được endpoint kiểm thử")
	}

	_, err = a.S3GetBucketPolicy("không-phải-uuid", "kho")
	errorCode(t, err, "InvalidArgument")
	_, err = a.S3PutBucketPolicy(p.ID, "kho", "")
	errorCode(t, err, "InvalidArgument")
	_, err = a.S3PutBucketPolicy(p.ID, "kho", "[]")
	errorCode(t, err, "InvalidPolicy")
	_, err = a.S3DeleteBucketPolicy(p.ID, "")
	errorCode(t, err, "InvalidArgument")
	if hits != 0 {
		t.Fatal("Đã gọi mạng khi tham số hoặc policy không hợp lệ")
	}

	got, err := a.S3GetBucketPolicy(p.ID, "kho")
	if err != nil || got != "" {
		t.Fatalf("Bucket chưa có policy phải trả chuỗi rỗng, nhận %q, %v", got, err)
	}
	ok, err := a.S3PutBucketPolicy(p.ID, "kho", "{\n  \"Version\": \"2012-10-17\"\n}")
	if err != nil || !ok || current != `{"Version":"2012-10-17"}` {
		t.Fatalf("Lưu policy qua App không đúng: ok=%v, current=%q, err=%v", ok, current, err)
	}
	got, err = a.S3GetBucketPolicy(p.ID, "kho")
	if err != nil || got != "{\n  \"Version\": \"2012-10-17\"\n}" {
		t.Fatalf("Đọc lại policy qua App không đúng: %q, %v", got, err)
	}
	ok, err = a.S3DeleteBucketPolicy(p.ID, "kho")
	if err != nil || !ok || current != "" {
		t.Fatalf("Xóa policy qua App không đúng: ok=%v, err=%v", ok, err)
	}
}
func TestAppTínhVàHủyDungLượngKhôngKhóaHồSơ(t *testing.T) {
	a, p := fixture(t)
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	original, _ := a.Backend.Profiles.Get(p.ID)
	original.Endpoint = server.URL
	p, err := a.ProfilesSave(original)
	if err != nil {
		t.Fatal("Không lưu được endpoint nội bộ")
	}
	ok, err := a.S3SizeStart(p.ID, "kho", "thư-mục/", false)
	if err != nil || !ok {
		t.Fatal("Không bắt đầu tính dung lượng được")
	}
	<-entered
	if ok, err = a.S3SizeCancel(p.ID, "kho", "thư-mục/"); err != nil || !ok {
		t.Fatal("Không hủy được việc tính dung lượng")
	}
	if ok, err = a.S3SizeCancel(p.ID, "kho", "thư-mục/"); err != nil || ok {
		t.Fatal("Hủy lặp lại phải trả false, không lỗi")
	}
}
