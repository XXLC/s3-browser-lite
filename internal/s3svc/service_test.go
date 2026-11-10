package s3svc

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"s3browser/internal/apperr"
	"s3browser/internal/profiles"
	"s3browser/internal/testutil"
)

type source map[string]profiles.Profile

func (s source) Get(id string) (profiles.Profile, error) {
	p, ok := s[id]
	if !ok {
		return profiles.Profile{}, apperr.New("ProfileNotFound", "Không có hồ sơ kiểm thử.")
	}
	return p, nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body == nil {
		r.Body = http.NoBody
	}
	return f(r)
}
func response(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/xml"}, "Etag": []string{"mã-kiểm-thử"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}
func setup(t *testing.T, handler roundTrip) (*Service, profiles.Profile) {
	t.Helper()
	p, err := profiles.Normalize(profiles.Profile{Name: "Kho", Endpoint: "https://s3.example.invalid", AccessKeyID: testutil.Random(), SecretAccessKey: testutil.Random()})
	if err != nil {
		t.Fatal("Không chuẩn bị được hồ sơ")
	}
	svc := New(source{p.ID: p}, &apperr.Filter{})
	c, err := svc.Client(p.ID)
	if err != nil {
		t.Fatal("Không dựng được client")
	}
	options := c.Options()
	options.HTTPClient = &http.Client{Transport: handler}
	options.RetryMaxAttempts = 1
	svc.clients[p.ID] = s3.New(options)
	t.Cleanup(svc.Close)
	return svc, p
}
func requireCode(t *testing.T, err error, want string) {
	t.Helper()
	own, ok := err.(*apperr.Error)
	if !ok || own.Code != want {
		t.Fatalf("Không nhận được mã lỗi %s", want)
	}
}
func TestDựngClientCáchLyTLSVàChecksum(t *testing.T) {
	p, _ := profiles.Normalize(profiles.Profile{Name: "Kho", Endpoint: "https://s3.example.invalid", AccessKeyID: testutil.Random(), SecretAccessKey: testutil.Random()})
	defaultTransport := http.DefaultTransport.(*http.Transport)
	before := defaultTransport.TLSClientConfig
	c, err := Build(p)
	if err != nil {
		t.Fatal("Không dựng được client")
	}
	defer closeClient(c)
	options := c.Options()
	if aws.ToString(options.BaseEndpoint) != p.Endpoint || !options.UsePathStyle || options.Region != "us-east-1" || options.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenRequired || options.ResponseChecksumValidation != aws.ResponseChecksumValidationWhenRequired {
		t.Fatal("Cấu hình client không đúng SPEC")
	}
	creds, err := options.Credentials.Retrieve(context.Background())
	if err != nil || creds.AccessKeyID != p.AccessKeyID || creds.SecretAccessKey != p.SecretAccessKey {
		t.Fatal("Không dùng đúng khóa tĩnh")
	}
	p.AllowInsecureTLS = true
	loose, _ := Build(p)
	defer closeClient(loose)
	a := options.HTTPClient.(*http.Client).Transport.(*http.Transport)
	b := loose.Options().HTTPClient.(*http.Client).Transport.(*http.Transport)
	if a == b || a == defaultTransport || a.TLSClientConfig.InsecureSkipVerify || !b.TLSClientConfig.InsecureSkipVerify || defaultTransport.TLSClientConfig != before {
		t.Fatal("Thiết lập TLS đã ảnh hưởng client khác")
	}
	p.SecretAccessKey = ""
	_, err = Build(p)
	requireCode(t, err, "CredentialsRequired")
}
func TestBộNhớĐệmVàVôHiệuHóaClient(t *testing.T) {
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		return response(r, 200, `<ListAllMyBucketsResult/>`), nil
	})
	first, _ := svc.Client(p.ID)
	second, _ := svc.Client(p.ID)
	if first != second {
		t.Fatal("Không dùng bộ nhớ đệm")
	}
	svc.Invalidate(p.ID)
	next, _ := svc.Client(p.ID)
	if first == next {
		t.Fatal("Không vô hiệu hóa client")
	}
	p.ID = testutil.Random()
	svc.profiles.(source)[p.ID] = p
	other, _ := svc.Client(p.ID)
	if next == other {
		t.Fatal("Hai hồ sơ dùng chung client")
	}
}
func TestPhânTrangDanhSáchĐốiTượng(t *testing.T) {
	calls := 0
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		calls++
		query := r.URL.Query()
		if query.Get("delimiter") != "/" || query.Get("max-keys") != "1000" || query.Get("prefix") != "a/" {
			t.Error("Tham số danh sách không đúng")
		}
		if calls == 1 {
			return response(r, 200, `<ListBucketResult><CommonPrefixes><Prefix>a/b/</Prefix></CommonPrefixes><Contents><Key>a/</Key><Size>0</Size></Contents><Contents><Key>a/tệp</Key><Size>12</Size><ETag>mã</ETag></Contents><NextContinuationToken>trang-hai</NextContinuationToken></ListBucketResult>`), nil
		}
		if query.Get("continuation-token") != "trang-hai" {
			t.Error("Token không được truyền")
		}
		return response(r, 200, `<ListBucketResult/>`), nil
	})
	first, err := svc.List(context.Background(), p.ID, "kho", "a/", "")
	if err != nil || len(first.Folders) != 1 || len(first.Objects) != 1 || first.Objects[0].Key != "a/tệp" || first.NextToken == nil {
		t.Fatal("Kết quả trang đầu không đúng")
	}
	second, err := svc.List(context.Background(), p.ID, "kho", "a/", *first.NextToken)
	if err != nil || second.NextToken != nil || second.Objects == nil || second.Folders == nil {
		t.Fatal("Trang cuối không đúng")
	}
}
func TestPhânTrangBucket(t *testing.T) {
	calls := 0
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("max-buckets") != "1000" {
			t.Error("Thiếu giới hạn trang bucket")
		}
		if calls == 1 {
			return response(r, 200, `<ListAllMyBucketsResult><Buckets><Bucket><Name>một</Name></Bucket></Buckets><ContinuationToken>tiếp</ContinuationToken></ListAllMyBucketsResult>`), nil
		}
		if r.URL.Query().Get("continuation-token") != "tiếp" {
			t.Error("Thiếu token bucket")
		}
		return response(r, 200, `<ListAllMyBucketsResult><Buckets><Bucket><Name>hai</Name></Bucket></Buckets></ListAllMyBucketsResult>`), nil
	})
	buckets, err := svc.ListBuckets(context.Background(), p.ID)
	if err != nil || len(buckets) != 2 || buckets[1].Name != "hai" {
		t.Fatal("Không ghép đúng các trang bucket")
	}
}
func TestXóaĐệQuyLiệtKêTrướcVàChiaLô(t *testing.T) {
	lists := 0
	batches := []int{}
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			lists++
			if len(batches) > 0 || r.URL.Query().Get("delimiter") != "" {
				t.Error("Xóa trước khi liệt kê xong hoặc có delimiter")
			}
			start, count := 0, 1000
			token := "<NextContinuationToken>tiếp</NextContinuationToken>"
			if lists == 2 {
				start = 1000
				count = 1001
				token = ""
				if r.URL.Query().Get("continuation-token") != "tiếp" {
					t.Error("Thiếu token đi bộ")
				}
			}
			var body strings.Builder
			body.WriteString("<ListBucketResult>" + token)
			for i := start; i < start+count; i++ {
				fmt.Fprintf(&body, "<Contents><Key>a/%d</Key><Size>1</Size></Contents>", i)
			}
			body.WriteString("</ListBucketResult>")
			return response(r, 200, body.String()), nil
		}
		var input struct {
			Objects []struct{ Key string } `xml:"Object"`
		}
		if err := xml.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error("Không giải mã được lô xóa")
		}
		batches = append(batches, len(input.Objects))
		return response(r, 200, `<DeleteResult/>`), nil
	})
	n, err := svc.DeletePrefix(context.Background(), p.ID, "kho", "a/")
	if err != nil || n != 2001 || fmt.Sprint(batches) != "[1000 1000 1]" {
		t.Fatal("Không xóa đúng lô 1000")
	}
	_, err = svc.DeletePrefix(context.Background(), p.ID, "kho", "")
	requireCode(t, err, "InvalidPrefix")
}
func TestXóaTừngPhầnGiữMãLỗi(t *testing.T) {
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		return response(r, 200, `<DeleteResult><Error><Key>a</Key><Code>AccessDenied</Code><Message>Máy chủ từ chối</Message></Error></DeleteResult>`), nil
	})
	_, err := svc.Delete(context.Background(), p.ID, "kho", []string{"a"})
	requireCode(t, err, "AccessDenied")
	if !strings.Contains(err.Error(), "1 đối tượng") || !strings.Contains(err.Error(), "Máy chủ từ chối") {
		t.Fatalf("Thông điệp thiếu số lượng hoặc lý do: %v", err)
	}
}
func TestTênBucketVàLocationConstraint(t *testing.T) {
	bodies := []string{}
	handler := roundTrip(func(r *http.Request) (*http.Response, error) {
		var b []byte
		if r.Body != nil {
			b, _ = io.ReadAll(r.Body)
		}
		bodies = append(bodies, string(b))
		return response(r, 200, ""), nil
	})
	svc, p := setup(t, handler)
	for _, config := range []struct {
		endpoint, region string
		location         bool
	}{{p.Endpoint, "ap-southeast-1", false}, {"", "us-east-1", false}, {"", "ap-southeast-1", true}} {
		p.Endpoint = config.endpoint
		p.Region = config.region
		svc.profiles.(source)[p.ID] = p
		c, _ := Build(p)
		opts := c.Options()
		opts.HTTPClient = &http.Client{Transport: handler}
		svc.clients[p.ID] = s3.New(opts)
		if _, err := svc.CreateBucket(context.Background(), p.ID, "kho-moi.01"); err != nil {
			t.Fatal("Không tạo được bucket hợp lệ")
		}
		body := bodies[len(bodies)-1]
		if strings.Contains(body, "LocationConstraint") != config.location {
			t.Fatal("LocationConstraint không đúng")
		}
	}
	calls := len(bodies)
	for _, name := range []string{"ab", "Kho-Hoa", "-kho", "kho-", "kho..moi", "192.168.1.1", "kho_moi", strings.Repeat("a", 64), "kho moi", ""} {
		_, err := svc.CreateBucket(context.Background(), p.ID, name)
		requireCode(t, err, "InvalidBucketName")
	}
	if len(bodies) != calls {
		t.Fatal("Đã gửi tên bucket sai")
	}
}
func TestDiChuyểnMãHóaNguồnVàChỉXóaSauSaoChép(t *testing.T) {
	calls := []string{}
	fail := false
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method)
		if r.Method == http.MethodPut {
			if r.Header.Get("X-Amz-Copy-Source") != "kho/th%C6%B0%20m%E1%BB%A5c/t%E1%BB%87p%20%2B.txt" {
				t.Error("CopySource không mã hóa đúng")
			}
			if fail {
				return response(r, 403, `<Error><Code>AccessDenied</Code><Message>Không sao chép được</Message></Error>`), nil
			}
			return response(r, 200, `<CopyObjectResult><ETag>mã</ETag></CopyObjectResult>`), nil
		}
		return response(r, 200, `<DeleteResult/>`), nil
	})
	_, err := svc.Move(context.Background(), p.ID, "kho", "thư mục/tệp +.txt", "mới.txt")
	if err != nil || fmt.Sprint(calls) != "[PUT POST]" {
		t.Fatal("Không sao chép trước khi xóa")
	}
	calls = nil
	fail = true
	_, err = svc.Move(context.Background(), p.ID, "kho", "thư mục/tệp +.txt", "mới.txt")
	if err == nil || fmt.Sprint(calls) != "[PUT]" {
		t.Fatal("Đã xóa nguồn khi sao chép lỗi")
	}
}
func TestDiChuyểnThưMụcChặnChồngNguồn(t *testing.T) {
	puts := 0
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			puts++
		}
		return response(r, 200, `<ListBucketResult><Contents><Key>a/b/x</Key></Contents><Contents><Key>a/b/b/x</Key></Contents></ListBucketResult>`), nil
	})
	for _, pair := range [][2]string{{"a/", "a/"}, {"a/", "a/b/"}, {"a/", "b"}, {"a/b/", "a/"}} {
		_, err := svc.Move(context.Background(), p.ID, "kho", pair[0], pair[1])
		requireCode(t, err, "InvalidMove")
	}
	if puts != 0 {
		t.Fatal("Đã ghi trước khi phát hiện nguồn chồng nhau")
	}
}
func TestTạoThưMụcHeadVàPresign(t *testing.T) {
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			if len(body) != 0 {
				t.Error("Đánh dấu thư mục không rỗng")
			}
			return response(r, 200, ""), nil
		}
		out := response(r, 200, "")
		out.Header.Set("Content-Length", "8")
		out.Header.Set("Content-Type", "text/plain")
		out.Header.Set("X-Amz-Meta-Mo-Ta", "thử nghiệm")
		return out, nil
	})
	if ok, err := svc.CreateFolder(context.Background(), p.ID, "kho", "a/"); err != nil || !ok {
		t.Fatal("Không tạo được thư mục")
	}
	head, err := svc.Head(context.Background(), p.ID, "kho", "a")
	if err != nil || head.Size != 8 || head.ContentType != "text/plain" || head.Metadata["mo-ta"] != "thử nghiệm" {
		t.Fatal("Head không đúng")
	}
	signed, err := svc.Presign(context.Background(), p.ID, "kho", "a", 3600)
	if err != nil || !strings.Contains(signed, "X-Amz-Expires=3600") {
		t.Fatal("Không ký được URL")
	}
	for _, expiry := range []int{0, 604801, -1} {
		_, err = svc.Presign(context.Background(), p.ID, "kho", "a", expiry)
		requireCode(t, err, "InvalidExpiry")
	}
}
func TestSDKThậtQuaHTTPNộiBộVàNhápGiữClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListAllMyBucketsResult><Buckets><Bucket><Name>kho-thử</Name></Bucket></Buckets></ListAllMyBucketsResult>`)
	}))
	defer server.Close()
	p, _ := profiles.Normalize(profiles.Profile{Name: "Kho", Endpoint: server.URL, AccessKeyID: testutil.Random(), SecretAccessKey: testutil.Random()})
	svc := New(source{p.ID: p}, &apperr.Filter{})
	defer svc.Close()
	cached, _ := svc.Client(p.ID)
	p.SecretAccessKey = ""
	list, err := svc.TestDraft(context.Background(), p)
	if err != nil || len(list) != 1 {
		t.Fatal("Không kiểm tra được bản nháp giữ khóa")
	}
	after, _ := svc.Client(p.ID)
	if after != cached {
		t.Fatal("Nháp đã thay client lưu")
	}
	p.ID = ""
	_, err = svc.TestDraft(context.Background(), p)
	requireCode(t, err, "CredentialsRequired")
}
func TestUploadMultipartHaiLuồngKhôngChecksumTùyChọn(t *testing.T) {
	var active, maximum, parts atomic.Int32
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Amz-Sdk-Checksum-Algorithm") != "" || r.Header.Get("X-Amz-Checksum-Crc32") != "" || r.Header.Get("X-Amz-Trailer") != "" {
			t.Error("Đã gửi checksum tùy chọn")
		}
		query := r.URL.Query()
		if _, ok := query["uploads"]; ok {
			return response(r, 200, `<InitiateMultipartUploadResult><Bucket>kho</Bucket><Key>tệp</Key><UploadId>lượt-thử</UploadId></InitiateMultipartUploadResult>`), nil
		}
		if r.Method == http.MethodPut && query.Get("uploadId") != "" {
			n := active.Add(1)
			for {
				old := maximum.Load()
				if n <= old || maximum.CompareAndSwap(old, n) {
					break
				}
			}
			defer active.Add(-1)
			time.Sleep(15 * time.Millisecond)
			_, _ = io.Copy(io.Discard, r.Body)
			parts.Add(1)
			return response(r, 200, ""), nil
		}
		if r.Method == http.MethodPost {
			return response(r, 200, `<CompleteMultipartUploadResult><ETag>mã</ETag></CompleteMultipartUploadResult>`), nil
		}
		return response(r, 200, ""), nil
	})
	size := int64(11 * 1024 * 1024)
	err := svc.Upload(context.Background(), p.ID, "kho", Upload{Key: "tệp", Body: strings.NewReader(strings.Repeat("a", int(size))), Size: size}, nil)
	if err != nil || parts.Load() != 3 || maximum.Load() != 2 {
		t.Fatal("Upload multipart không dùng đúng hai luồng")
	}
}
func TestBucketPolicyĐọcGhiXóaVàLọcSecret(t *testing.T) {
	calls := 0
	var lastMethod, lastBody string
	mode := "has-policy"
	var p profiles.Profile
	svc, p := setup(t, func(r *http.Request) (*http.Response, error) {
		calls++
		lastMethod = r.Method
		if _, ok := r.URL.Query()["policy"]; !ok {
			t.Error("Thiếu tham số truy vấn policy")
		}
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			lastBody = string(b)
		}
		switch mode {
		case "has-policy":
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow"}]}`)),
				Request:    r,
			}, nil
		case "no-policy":
			return response(r, 404, `<Error><Code>NoSuchBucketPolicy</Code><Message>Chưa cấu hình</Message></Error>`), nil
		case "fake-message":
			return response(r, 403, `<Error><Code>AccessDenied</Code><Message>NoSuchBucketPolicy `+p.SecretAccessKey+` `+p.AccessKeyID+`</Message></Error>`), nil
		case "non-json":
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("nội dung nguyên văn { không phải json")),
				Request:    r,
			}, nil
		case "net-error":
			return nil, fmt.Errorf("lỗi kết nối %s %s", p.AccessKeyID, p.SecretAccessKey)
		default:
			return response(r, 204, ""), nil
		}
	})

	got, err := svc.GetBucketPolicy(context.Background(), p.ID, "kho")
	wantPretty := "{\n  \"Version\": \"2012-10-17\",\n  \"Statement\": [\n    {\n      \"Effect\": \"Allow\"\n    }\n  ]\n}"
	if err != nil || lastMethod != http.MethodGet || got != wantPretty {
		t.Fatalf("Định dạng policy không đúng: %q, err=%v", got, err)
	}

	mode = "no-policy"
	got, err = svc.GetBucketPolicy(context.Background(), p.ID, "kho")
	if err != nil || got != "" {
		t.Fatalf("NoSuchBucketPolicy phải trả chuỗi rỗng và nil, nhận %q, %v", got, err)
	}

	mode = "non-json"
	got, err = svc.GetBucketPolicy(context.Background(), p.ID, "kho")
	if err != nil || got != "nội dung nguyên văn { không phải json" {
		t.Fatalf("Nội dung không phải JSON phải trả nguyên văn, nhận %q, %v", got, err)
	}

	mode = "fake-message"
	_, err = svc.GetBucketPolicy(context.Background(), p.ID, "kho")
	if err == nil {
		t.Fatal("Không được nhận diện NoSuchBucketPolicy qua chuỗi Message")
	}
	described := svc.Filter.Describe(err)
	if described.Code != "AccessDenied" || strings.Contains(described.Error(), p.SecretAccessKey) || strings.Contains(described.Error(), p.AccessKeyID) {
		t.Fatal("Chưa lọc khóa bí mật trong lỗi máy chủ")
	}

	beforeCalls := calls
	marker := "chuỗi-nhạy-cảm-trong-policy"
	invalidPolicies := []string{
		"",
		"   \n\t  ",
		`{"a":"` + strings.Repeat("x", 20480) + `"}`,
		`{sai-cu-phap ` + marker,
		`[]`,
		`[{"Version":"2012-10-17"}]`,
		`"chuỗi"`,
		`123`,
		`true`,
		`null`,
	}
	for _, bad := range invalidPolicies {
		err = svc.PutBucketPolicy(context.Background(), p.ID, "kho", bad)
		requireCode(t, err, "InvalidPolicy")
		if strings.Contains(err.Error(), marker) {
			t.Fatal("Thông điệp lỗi đã in nội dung policy")
		}
	}
	if calls != beforeCalls {
		t.Fatal("PutBucketPolicy đã gọi mạng khi dữ liệu cục bộ không hợp lệ")
	}

	mode = "ok"
	err = svc.PutBucketPolicy(context.Background(), p.ID, "kho", "{\n  \"Version\": \"2012-10-17\",\n  \"Statement\": []\n}")
	if err != nil || lastMethod != http.MethodPut || lastBody != `{"Version":"2012-10-17","Statement":[]}` {
		t.Fatalf("PutBucketPolicy không gửi đúng JSON nén gọn: %q, err=%v", lastBody, err)
	}

	err = svc.DeleteBucketPolicy(context.Background(), p.ID, "kho")
	if err != nil || lastMethod != http.MethodDelete {
		t.Fatalf("DeleteBucketPolicy không đúng: method=%s, err=%v", lastMethod, err)
	}

	mode = "net-error"
	for _, fn := range []func() error{
		func() error { _, e := svc.GetBucketPolicy(context.Background(), p.ID, "kho"); return e },
		func() error { return svc.PutBucketPolicy(context.Background(), p.ID, "kho", `{}`) },
		func() error { return svc.DeleteBucketPolicy(context.Background(), p.ID, "kho") },
	} {
		netErr := fn()
		if netErr == nil {
			t.Fatal("Thiếu lỗi mạng")
		}
		clean := svc.Filter.Describe(netErr).Error()
		if strings.Contains(clean, p.SecretAccessKey) || strings.Contains(clean, p.AccessKeyID) {
			t.Fatal("Lỗi mạng chưa lọc khóa truy cập")
		}
	}
}
