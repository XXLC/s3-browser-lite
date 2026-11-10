package s3svc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"s3browser/internal/apperr"
	"s3browser/internal/profiles"
)

type Bucket struct {
	Name         string     `json:"name"`
	CreationDate *time.Time `json:"creationDate,omitempty"`
}
type Folder struct {
	Prefix string `json:"prefix"`
}
type Object struct {
	Key          string     `json:"key"`
	Size         int64      `json:"size"`
	LastModified *time.Time `json:"lastModified"`
	ETag         string     `json:"etag"`
}
type ListResult struct {
	Folders   []Folder `json:"folders"`
	Objects   []Object `json:"objects"`
	NextToken *string  `json:"nextToken"`
}
type HeadResult struct {
	Size         int64             `json:"size"`
	ETag         string            `json:"etag"`
	ContentType  string            `json:"contentType"`
	Metadata     map[string]string `json:"metadata"`
	LastModified *time.Time        `json:"lastModified"`
}
type Download struct {
	Body        io.ReadCloser
	Size        int64
	ContentType string
	Metadata    map[string]string
}
type Upload struct {
	Key         string
	Body        io.Reader
	Size        int64
	ContentType string
	Metadata    map[string]string
}
type ProfileSource interface {
	Get(string) (profiles.Profile, error)
}
type Service struct {
	mu       sync.Mutex
	profiles ProfileSource
	clients  map[string]*s3.Client
	Filter   *apperr.Filter
	OnWrite  func(id, bucket string)
}

func (s *Service) notifyWrite(id, bucket string) {
	if s.OnWrite != nil {
		s.OnWrite(id, bucket)
	}
}

func New(source ProfileSource, filter *apperr.Filter) *Service {
	return &Service{profiles: source, clients: map[string]*s3.Client{}, Filter: filter}
}
func Build(p profiles.Profile) (*s3.Client, error) {
	if p.AccessKeyID == "" || p.SecretAccessKey == "" {
		return nil, apperr.New("CredentialsRequired", "Vui lòng nhập mã khóa truy cập và khóa bí mật cho hồ sơ.")
	}
	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: p.AllowInsecureTLS},
	}
	cfg := aws.Config{Region: p.Region, Credentials: credentials.NewStaticCredentialsProvider(p.AccessKeyID, p.SecretAccessKey, ""), HTTPClient: &http.Client{Transport: transport}, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = p.PathStyle()
		if p.Endpoint != "" {
			o.BaseEndpoint = aws.String(p.Endpoint)
		}
	}), nil
}
func (s *Service) Client(id string) (*s3.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.clients[id]; c != nil {
		return c, nil
	}
	p, err := s.profiles.Get(id)
	if err != nil {
		return nil, err
	}
	s.Filter.Remember(p.AccessKeyID, p.SecretAccessKey)
	c, err := Build(p)
	if err == nil {
		s.clients[id] = c
	}
	return c, err
}
func closeClient(c *s3.Client) {
	if c == nil {
		return
	}
	if h, ok := c.Options().HTTPClient.(interface{ CloseIdleConnections() }); ok {
		h.CloseIdleConnections()
	}
}
func (s *Service) Invalidate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	closeClient(s.clients[id])
	delete(s.clients, id)
}
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.clients {
		closeClient(c)
		delete(s.clients, id)
	}
}
func listBuckets(ctx context.Context, c *s3.Client) ([]Bucket, error) {
	result := []Bucket{}
	var token *string
	for {
		out, err := c.ListBuckets(ctx, &s3.ListBucketsInput{MaxBuckets: aws.Int32(1000), ContinuationToken: token})
		if err != nil {
			return nil, err
		}
		for _, b := range out.Buckets {
			result = append(result, Bucket{aws.ToString(b.Name), b.CreationDate})
		}
		token = out.ContinuationToken
		if aws.ToString(token) == "" {
			return result, nil
		}
	}
}
func (s *Service) ListBuckets(ctx context.Context, id string) ([]Bucket, error) {
	c, err := s.Client(id)
	if err != nil {
		return nil, err
	}
	return listBuckets(ctx, c)
}
func (s *Service) TestDraft(ctx context.Context, p profiles.Profile) ([]Bucket, error) {
	s.Filter.Remember(p.AccessKeyID, p.SecretAccessKey)
	savedID := p.ID
	p, err := profiles.Normalize(p)
	if err != nil {
		return nil, err
	}
	if p.SecretAccessKey != "" {
		s.Filter.Remember(p.SecretAccessKey)
	} else if savedID != "" {
		saved, e := s.profiles.Get(savedID)
		if e == nil {
			p.SecretAccessKey = saved.SecretAccessKey
		} else {
			return nil, e
		}
	}
	s.Filter.Remember(p.SecretAccessKey)
	c, err := Build(p)
	if err != nil {
		return nil, err
	}
	defer closeClient(c)
	return listBuckets(ctx, c)
}
func (s *Service) List(ctx context.Context, id, bucket, prefix, token string) (ListResult, error) {
	c, err := s.Client(id)
	if err != nil {
		return ListResult{}, err
	}
	in := &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: &prefix, Delimiter: aws.String("/"), MaxKeys: aws.Int32(1000)}
	if token != "" {
		in.ContinuationToken = &token
	}
	out, err := c.ListObjectsV2(ctx, in)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Folders: []Folder{}, Objects: []Object{}}
	if aws.ToString(out.NextContinuationToken) != "" {
		result.NextToken = out.NextContinuationToken
	}
	for _, f := range out.CommonPrefixes {
		result.Folders = append(result.Folders, Folder{aws.ToString(f.Prefix)})
	}
	for _, o := range out.Contents {
		if aws.ToString(o.Key) != prefix {
			result.Objects = append(result.Objects, object(o))
		}
	}
	return result, nil
}
func object(o types.Object) Object {
	return Object{aws.ToString(o.Key), aws.ToInt64(o.Size), o.LastModified, aws.ToString(o.ETag)}
}
func (s *Service) Walk(ctx context.Context, id, bucket, prefix string, visit func(Object) error) error {
	c, err := s.Client(id)
	if err != nil {
		return err
	}
	var token *string
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		out, e := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: &prefix, MaxKeys: aws.Int32(1000), ContinuationToken: token})
		if e != nil {
			return e
		}
		for _, o := range out.Contents {
			if e = visit(object(o)); e != nil {
				return e
			}
		}
		token = out.NextContinuationToken
		if aws.ToString(token) == "" {
			return nil
		}
	}
}
func (s *Service) Head(ctx context.Context, id, bucket, key string) (HeadResult, error) {
	c, err := s.Client(id)
	if err != nil {
		return HeadResult{}, err
	}
	out, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return HeadResult{}, err
	}
	metadata := out.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	return HeadResult{aws.ToInt64(out.ContentLength), aws.ToString(out.ETag), aws.ToString(out.ContentType), metadata, out.LastModified}, nil
}

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var ipName = regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)

func (s *Service) CreateBucket(ctx context.Context, id, name string) (Bucket, error) {
	if !bucketName.MatchString(name) || strings.Contains(name, "..") || ipName.MatchString(name) {
		return Bucket{}, apperr.New("InvalidBucketName", "Tên bucket phải dài 3 đến 63 ký tự, chỉ gồm chữ thường, số, dấu chấm và dấu gạch ngang, bắt đầu và kết thúc bằng chữ hoặc số, không có hai dấu chấm liền nhau và không có dạng địa chỉ IP.")
	}
	p, err := s.profiles.Get(id)
	if err != nil {
		return Bucket{}, err
	}
	c, err := s.Client(id)
	if err != nil {
		return Bucket{}, err
	}
	in := &s3.CreateBucketInput{Bucket: &name}
	if p.Endpoint == "" && p.Region != "us-east-1" {
		in.CreateBucketConfiguration = &types.CreateBucketConfiguration{LocationConstraint: types.BucketLocationConstraint(p.Region)}
	}
	_, err = c.CreateBucket(ctx, in)
	return Bucket{Name: name}, err
}
func (s *Service) GetBucketPolicy(ctx context.Context, id, bucket string) (string, error) {
	c, err := s.Client(id)
	if err != nil {
		return "", err
	}
	out, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: &bucket})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchBucketPolicy" {
			return "", nil
		}
		return "", err
	}
	raw := aws.ToString(out.Policy)
	if raw == "" {
		return "", nil
	}
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(raw), "", "  ") != nil {
		return raw, nil
	}
	return buf.String(), nil
}
func (s *Service) PutBucketPolicy(ctx context.Context, id, bucket, policy string) error {
	if strings.TrimSpace(policy) == "" {
		return apperr.New("InvalidPolicy", "Nội dung policy không được để trống.")
	}
	if len(policy) > 20480 {
		return apperr.New("InvalidPolicy", "Nội dung policy vượt quá giới hạn 20480 byte của S3.")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return apperr.New("InvalidPolicy", "Policy phải là một đối tượng JSON hợp lệ.")
		}
		return apperr.New("InvalidPolicy", "Nội dung policy không phải là JSON hợp lệ.")
	}
	if doc == nil {
		return apperr.New("InvalidPolicy", "Policy phải là một đối tượng JSON hợp lệ.")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(policy)); err != nil {
		return apperr.New("InvalidPolicy", "Nội dung policy không phải là JSON hợp lệ.")
	}
	c, err := s.Client(id)
	if err != nil {
		return err
	}
	body := compact.String()
	_, err = c.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: &bucket, Policy: &body})
	return err
}
func (s *Service) DeleteBucketPolicy(ctx context.Context, id, bucket string) error {
	c, err := s.Client(id)
	if err != nil {
		return err
	}
	_, err = c.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: &bucket})
	return err
}
func (s *Service) Delete(ctx context.Context, id, bucket string, keys []string) (int, error) {
	c, err := s.Client(id)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for offset := 0; offset < len(keys); offset += 1000 {
		end := min(offset+1000, len(keys))
		objects := make([]types.ObjectIdentifier, 0, end-offset)
		for _, key := range keys[offset:end] {
			objects = append(objects, types.ObjectIdentifier{Key: aws.String(key)})
		}
		out, e := c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: &bucket, Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}})
		if e != nil {
			return deleted, e
		}
		if len(out.Errors) > 0 {
			code := aws.ToString(out.Errors[0].Code)
			if code == "" {
				code = "DeleteFailed"
			}
			message := aws.ToString(out.Errors[0].Message)
			if message == "" {
				message = "Máy chủ từ chối thao tác."
			}
			return deleted, apperr.New(code, "Không xóa được "+strconv.Itoa(len(out.Errors))+" đối tượng: "+message)
		}
		deleted += len(objects)
	}
	s.notifyWrite(id, bucket)
	return deleted, nil
}
func (s *Service) DeletePrefix(ctx context.Context, id, bucket, prefix string) (int, error) {
	if prefix == "" || !strings.HasSuffix(prefix, "/") {
		return 0, apperr.New("InvalidPrefix", "Chỉ xóa đệ quy một thư mục có tên kết thúc bằng dấu /.")
	}
	keys := []string{}
	err := s.Walk(ctx, id, bucket, prefix, func(o Object) error { keys = append(keys, o.Key); return nil })
	if err != nil {
		return 0, err
	}
	return s.Delete(ctx, id, bucket, keys)
}
func (s *Service) CreateFolder(ctx context.Context, id, bucket, prefix string) (bool, error) {
	if prefix == "" || !strings.HasSuffix(prefix, "/") {
		return false, apperr.New("InvalidPrefix", "Tên thư mục phải kết thúc bằng dấu /.")
	}
	c, err := s.Client(id)
	if err != nil {
		return false, err
	}
	_, err = c.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &prefix, Body: strings.NewReader("")})
	if err != nil {
		return false, err
	}
	s.notifyWrite(id, bucket)
	return true, nil
}
func encode(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
func (s *Service) Move(ctx context.Context, id, bucket, from, to string) (int, error) {
	folder := strings.HasSuffix(from, "/")
	if from == to || (folder && (!strings.HasSuffix(to, "/") || strings.HasPrefix(to, from))) {
		return 0, apperr.New("InvalidMove", "Không thể chuyển thư mục vào chính nó; tên thư mục đích phải kết thúc bằng dấu /.")
	}
	keys := []string{}
	if folder {
		if err := s.Walk(ctx, id, bucket, from, func(o Object) error { keys = append(keys, o.Key); return nil }); err != nil {
			return 0, err
		}
	} else {
		keys = append(keys, from)
	}
	source := map[string]bool{}
	for _, k := range keys {
		source[k] = true
	}
	targets := make([]string, len(keys))
	for i, k := range keys {
		target := to
		if folder {
			target += strings.TrimPrefix(k, from)
		}
		if source[target] {
			return 0, apperr.New("InvalidMove", "Đường dẫn đích trùng với một đối tượng nguồn. Hãy dùng một thư mục đích tách biệt.")
		}
		targets[i] = target
	}
	c, err := s.Client(id)
	if err != nil {
		return 0, err
	}
	for i, k := range keys {
		parts := strings.Split(k, "/")
		for j := range parts {
			parts[j] = encode(parts[j])
		}
		copySource := encode(bucket) + "/" + strings.Join(parts, "/")
		if _, err = c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: &bucket, Key: &targets[i], CopySource: &copySource}); err != nil {
			return 0, err
		}
	}
	return s.Delete(ctx, id, bucket, keys)
}
func (s *Service) Presign(ctx context.Context, id, bucket, key string, expiry int) (string, error) {
	if expiry < 1 || expiry > 604800 {
		return "", apperr.New("InvalidExpiry", "Thời hạn phải từ 1 đến 604800 giây.")
	}
	c, err := s.Client(id)
	if err != nil {
		return "", err
	}
	out, err := s3.NewPresignClient(c).PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key}, func(o *s3.PresignOptions) { o.Expires = time.Duration(expiry) * time.Second })
	if err != nil {
		return "", err
	}
	return out.URL, nil
}
func (s *Service) Get(ctx context.Context, id, bucket, key string) (Download, error) {
	c, err := s.Client(id)
	if err != nil {
		return Download{}, err
	}
	out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return Download{}, err
	}
	return Download{out.Body, aws.ToInt64(out.ContentLength), aws.ToString(out.ContentType), out.Metadata}, nil
}

type progressReader struct {
	reader   io.Reader
	loaded   int64
	progress func(int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.loaded += int64(n)
	r.progress(r.loaded)
	return n, err
}
func (s *Service) Upload(ctx context.Context, id, bucket string, in Upload, progress func(int64)) error {
	c, err := s.Client(id)
	if err != nil {
		return err
	}
	uploader := manager.NewUploader(c, func(u *manager.Uploader) {
		u.Concurrency = 2
		u.LeavePartsOnError = false
		u.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	body := in.Body
	if progress != nil {
		body = &progressReader{reader: body, progress: progress}
	}
	input := &s3.PutObjectInput{Bucket: &bucket, Key: &in.Key, Body: body, Metadata: in.Metadata}
	if in.Size >= 0 {
		input.ContentLength = &in.Size
	}
	if in.ContentType != "" {
		input.ContentType = &in.ContentType
	}
	_, err = uploader.Upload(ctx, input)
	if err != nil && ctx.Err() != nil {
		var failure manager.MultiUploadFailure
		if errors.As(err, &failure) {
			// SDK dùng lại context đã hủy khi dọn multipart; thử dọn bằng context riêng.
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_, _ = c.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{Bucket: &bucket, Key: &in.Key, UploadId: aws.String(failure.UploadID())})
		}
	}
	return err
}
