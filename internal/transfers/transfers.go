package transfers

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"s3browser/internal/apperr"
	"s3browser/internal/s3svc"
)

type Backend interface {
	Walk(context.Context, string, string, string, func(s3svc.Object) error) error
	Head(context.Context, string, string, string) (s3svc.HeadResult, error)
	Get(context.Context, string, string, string) (s3svc.Download, error)
	Upload(context.Context, string, string, s3svc.Upload, func(int64)) error
}
type Transfers struct {
	*Queue
	service Backend
	OnWrite func(id, bucket string)
}

func New(service Backend, filter *apperr.Filter, update func(Job)) *Transfers {
	return &Transfers{Queue: NewQueue(filter, update), service: service}
}
func (t *Transfers) Upload(id, bucket, prefix string, paths []string) ([]Job, error) {
	jobs := make([]Job, 0, len(paths))
	for _, path := range paths {
		job, err := t.Add("upload", filepath.Base(path), []string{id}, func(ctx context.Context, progress Progress) error {
			files := []localFile{}
			if err := localFiles(ctx, path, filepath.Base(path), &files); err != nil {
				return err
			}
			var total, done int64
			for _, file := range files {
				total += file.size
			}
			progress(0, total)
			for _, file := range files {
				if err := ctx.Err(); err != nil {
					return err
				}
				var body io.ReadCloser = io.NopCloser(strings.NewReader(""))
				if !file.directory {
					var err error
					body, err = os.Open(file.path)
					if err != nil {
						return err
					}
				}
				stop := context.AfterFunc(ctx, func() { _ = body.Close() })
				err := t.service.Upload(ctx, id, bucket, s3svc.Upload{Key: prefix + file.key, Body: body, Size: file.size}, func(n int64) { progress(done+n, total) })
				stop()
				_ = body.Close()
				if err != nil {
					return err
				}
				done += file.size
				progress(done, total)
			}
			if t.OnWrite != nil {
				t.OnWrite(id, bucket)
			}
			return nil
		})
		if err != nil {
			return jobs, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
func (t *Transfers) expand(ctx context.Context, id, bucket string, keys []string) ([]s3svc.Object, error) {
	files := []s3svc.Object{}
	positions := map[string]int{}
	add := func(o s3svc.Object) error {
		if i, ok := positions[o.Key]; ok {
			files[i] = o
		} else {
			positions[o.Key] = len(files)
			files = append(files, o)
		}
		return nil
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.HasSuffix(key, "/") {
			if err := t.service.Walk(ctx, id, bucket, key, add); err != nil {
				return nil, err
			}
		} else {
			head, err := t.service.Head(ctx, id, bucket, key)
			if err != nil {
				return nil, err
			}
			_ = add(s3svc.Object{Key: key, Size: head.Size})
		}
	}
	return files, nil
}

type counter struct {
	reader   io.Reader
	ctx      context.Context
	done     *int64
	total    int64
	progress Progress
}

func (c *counter) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.reader.Read(b)
	*c.done += int64(n)
	c.progress(*c.done, c.total)
	return n, err
}
func downloadFile(ctx context.Context, backend Backend, id, bucket, root, target string, file s3svc.Object, done *int64, total int64, progress Progress) error {
	if err := EnsureSafeDirectory(root, filepath.Dir(target)); err != nil {
		return err
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		if err == nil {
			return apperr.New("FileExists", "Tệp đích đã tồn tại. Chọn thư mục khác để tránh ghi đè.")
		}
		return err
	}
	body, err := backend.Get(ctx, id, bucket, file.Key)
	if err != nil {
		return err
	}
	if body.Body == nil {
		return apperr.New("EmptyResponse", "Máy chủ không trả luồng dữ liệu.")
	}
	defer body.Body.Close()
	stop := context.AfterFunc(ctx, func() { _ = body.Body.Close() })
	defer stop()
	f, err := os.CreateTemp(filepath.Dir(target), ".s3-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	_, err = io.Copy(f, &counter{body.Body, ctx, done, total, progress})
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = EnsureSafeDirectory(root, filepath.Dir(target)); err != nil {
		return err
	}
	// Công bố tệp đã hoàn thành bằng liên kết cứng để không ghi đè tệp vừa xuất hiện.
	if err = os.Link(f.Name(), target); os.IsExist(err) {
		return apperr.New("FileExists", "Tệp đích đã tồn tại. Chọn thư mục khác để tránh ghi đè.")
	}
	return err
}
func (t *Transfers) Download(id, bucket string, keys []string, root string) ([]Job, error) {
	completed := map[string]bool{}
	job, err := t.Add("download", strings.Join(keys, ", "), []string{id}, func(ctx context.Context, progress Progress) error {
		files, err := t.expand(ctx, id, bucket, keys)
		if err != nil {
			return err
		}
		destinations := map[string]bool{}
		targets := make([]string, len(files))
		var total, done int64
		for i, file := range files {
			target, e := SafeDownloadPath(root, file.Key)
			if e != nil {
				return e
			}
			normalized := strings.ToLower(target)
			if destinations[normalized] {
				return apperr.New("PathCollision", "Tên đối tượng trùng nhau trên hệ thống tệp Windows.")
			}
			destinations[normalized] = true
			targets[i] = target
			total += file.Size
		}
		progress(0, total)
		for i, file := range files {
			if err = ctx.Err(); err != nil {
				return err
			}
			if strings.HasSuffix(file.Key, "/") {
				if err = EnsureSafeDirectory(root, targets[i]); err != nil {
					return err
				}
				continue
			}
			if completed[file.Key] {
				done += file.Size
				progress(done, total)
				continue
			}
			if err = downloadFile(ctx, t.service, id, bucket, root, targets[i], file, &done, total, progress); err != nil {
				return err
			}
			completed[file.Key] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return []Job{job}, nil
}
func (t *Transfers) Copy(srcID, srcBucket string, keys []string, dstID, dstBucket, prefix string) ([]Job, error) {
	job, err := t.Add("copy", strings.Join(keys, ", "), []string{srcID, dstID}, func(ctx context.Context, progress Progress) error {
		files, err := t.expand(ctx, srcID, srcBucket, keys)
		if err != nil {
			return err
		}
		parents := append([]string(nil), keys...)
		sort.SliceStable(parents, func(i, j int) bool { return len(parents[i]) < len(parents[j]) })
		targets := make([]string, len(files))
		destinations := map[string]bool{}
		sources := map[string]bool{}
		var total, done int64
		for _, file := range files {
			sources[file.Key] = true
			total += file.Size
		}
		for i, file := range files {
			parent := ""
			for _, candidate := range parents {
				if file.Key == candidate || (strings.HasSuffix(candidate, "/") && strings.HasPrefix(file.Key, candidate)) {
					parent = candidate
					break
				}
			}
			if parent == "" {
				return apperr.New("InvalidCopy", "Máy chủ trả đối tượng nằm ngoài nguồn sao chép.")
			}
			parts := strings.Split(strings.TrimSuffix(parent, "/"), "/")
			target := prefix + parts[len(parts)-1]
			if strings.HasSuffix(parent, "/") {
				target += "/" + strings.TrimPrefix(file.Key, parent)
			}
			if srcID == dstID && srcBucket == dstBucket && sources[target] {
				return apperr.New("InvalidCopy", "Đường dẫn đích trùng với một đối tượng nguồn đang sao chép.")
			}
			if destinations[target] {
				return apperr.New("CopyCollision", "Nhiều đối tượng nguồn có cùng tên đích. Vui lòng sao chép riêng từng mục.")
			}
			destinations[target] = true
			targets[i] = target
		}
		progress(0, total)
		for i, file := range files {
			if err = ctx.Err(); err != nil {
				return err
			}
			object, e := t.service.Get(ctx, srcID, srcBucket, file.Key)
			if e != nil {
				return e
			}
			if object.Body == nil {
				object.Body = io.NopCloser(strings.NewReader(""))
			}
			stop := context.AfterFunc(ctx, func() { _ = object.Body.Close() })
			e = t.service.Upload(ctx, dstID, dstBucket, s3svc.Upload{Key: targets[i], Body: object.Body, Size: object.Size, ContentType: object.ContentType, Metadata: object.Metadata}, func(n int64) { progress(done+n, total) })
			stop()
			_ = object.Body.Close()
			if e != nil {
				return e
			}
			done += file.Size
			progress(done, total)
		}
		if t.OnWrite != nil {
			t.OnWrite(dstID, dstBucket)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return []Job{job}, nil
}
