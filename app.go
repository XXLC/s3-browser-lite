package main

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"s3browser/internal/apperr"
	"s3browser/internal/backend"
	"s3browser/internal/dialogs"
	"s3browser/internal/profiles"
	"s3browser/internal/s3svc"
	"s3browser/internal/sizer"
	"s3browser/internal/transfers"
	"s3browser/internal/validate"
)

type App struct {
	mu      sync.RWMutex
	ctx     context.Context
	backend *backend.Backend
}

func NewApp() *App {
	a := &App{}
	a.backend = backend.Default(func(job transfers.Job) {
		a.mu.RLock()
		ctx := a.ctx
		a.mu.RUnlock()
		if ctx != nil {
			runtime.EventsEmit(ctx, "transfers:update", job)
		}
	}, func(ev sizer.Event) {
		a.mu.RLock()
		ctx := a.ctx
		a.mu.RUnlock()
		if ctx != nil {
			runtime.EventsEmit(ctx, "size:update", ev)
		}
	})
	return a
}
func (a *App) startup(ctx context.Context) { a.mu.Lock(); a.ctx = ctx; a.mu.Unlock() }
func (a *App) shutdown(context.Context)    { a.backend.Close(); a.mu.Lock(); a.ctx = nil; a.mu.Unlock() }
func call[T any](a *App, check error, ids []string, fn func() (T, error)) (T, error) {
	var zero T
	if a.backend.Err != nil {
		return zero, a.backend.Filter.Describe(a.backend.Err)
	}
	if check != nil {
		return zero, a.backend.Filter.Describe(check)
	}
	if len(ids) > 0 {
		done, err := a.backend.Begin(ids...)
		if err != nil {
			return zero, a.backend.Filter.Describe(err)
		}
		defer done()
	}
	result, err := fn()
	if err != nil {
		return zero, a.backend.Filter.Describe(err)
	}
	return result, nil
}
func target(id, bucket string) error { return validate.First(validate.ID(id), validate.Bucket(bucket)) }
func (a *App) ProfilesList() ([]profiles.Profile, error) {
	return call(a, nil, nil, a.backend.Profiles.List)
}
func (a *App) ProfilesSave(p profiles.Profile) (profiles.Profile, error) {
	return call(a, nil, nil, func() (profiles.Profile, error) { return a.backend.Save(p) })
}
func (a *App) ProfilesRemove(id string) (bool, error) {
	return call(a, validate.ID(id), nil, func() (bool, error) { return a.backend.Remove(id) })
}
func (a *App) ProfilesDuplicate(id string) (profiles.Profile, error) {
	return call(a, validate.ID(id), []string{id}, func() (profiles.Profile, error) { return a.backend.Profiles.Duplicate(id) })
}
func (a *App) ProfilesTestID(id string) ([]s3svc.Bucket, error) { return a.S3ListBuckets(id) }
func (a *App) ProfilesTestDraft(p profiles.Profile) ([]s3svc.Bucket, error) {
	a.backend.Filter.Remember(p.AccessKeyID, p.SecretAccessKey)
	var ids []string
	if p.ID != "" {
		ids = []string{p.ID}
	}
	return call(a, nil, ids, func() ([]s3svc.Bucket, error) { return a.backend.S3.TestDraft(a.backend.Context(), p) })
}
func (a *App) ProfilesImport(input string) ([]profiles.Profile, error) {
	return call(a, validate.Text(input, "JSON nhập", 2*1024*1024, false), nil, func() ([]profiles.Profile, error) { return a.backend.Profiles.Import(input) })
}
func (a *App) ProfilesExport() (string, error) { return call(a, nil, nil, a.backend.Profiles.Export) }
func (a *App) S3ListBuckets(id string) ([]s3svc.Bucket, error) {
	return call(a, validate.ID(id), []string{id}, func() ([]s3svc.Bucket, error) { return a.backend.S3.ListBuckets(a.backend.Context(), id) })
}
func (a *App) S3CreateBucket(id, name string) (s3svc.Bucket, error) {
	return call(a, validate.First(validate.ID(id), validate.Text(name, "Tên bucket", 63, false)), []string{id}, func() (s3svc.Bucket, error) { return a.backend.S3.CreateBucket(a.backend.Context(), id, name) })
}
func (a *App) S3GetBucketPolicy(id, bucket string) (string, error) {
	return call(a, validate.First(target(id, bucket)), []string{id}, func() (string, error) {
		return a.backend.S3.GetBucketPolicy(a.backend.Context(), id, bucket)
	})
}
func (a *App) S3PutBucketPolicy(id, bucket, policy string) (bool, error) {
	return call(a, validate.First(target(id, bucket), validate.Text(policy, "Policy", 20480, false)), []string{id}, func() (bool, error) {
		err := a.backend.S3.PutBucketPolicy(a.backend.Context(), id, bucket, policy)
		return err == nil, err
	})
}
func (a *App) S3DeleteBucketPolicy(id, bucket string) (bool, error) {
	return call(a, validate.First(target(id, bucket)), []string{id}, func() (bool, error) {
		err := a.backend.S3.DeleteBucketPolicy(a.backend.Context(), id, bucket)
		return err == nil, err
	})
}
func (a *App) S3List(id, bucket, prefix, token string) (s3svc.ListResult, error) {
	return call(a, validate.First(target(id, bucket), validate.Prefix(prefix), validate.Text(token, "Token phân trang", 16384, true)), []string{id}, func() (s3svc.ListResult, error) {
		return a.backend.S3.List(a.backend.Context(), id, bucket, prefix, token)
	})
}
func (a *App) S3Head(id, bucket, key string) (s3svc.HeadResult, error) {
	return call(a, validate.First(target(id, bucket), validate.Key(key)), []string{id}, func() (s3svc.HeadResult, error) { return a.backend.S3.Head(a.backend.Context(), id, bucket, key) })
}
func (a *App) S3Delete(id, bucket string, keys []string) (int, error) {
	return call(a, validate.First(target(id, bucket), validate.Strings(keys, 10000, validate.Key)), []string{id}, func() (int, error) { return a.backend.S3.Delete(a.backend.Context(), id, bucket, keys) })
}
func (a *App) S3DeletePrefix(id, bucket, prefix string) (int, error) {
	return call(a, validate.First(target(id, bucket), validate.Key(prefix), validate.Prefix(prefix)), []string{id}, func() (int, error) { return a.backend.S3.DeletePrefix(a.backend.Context(), id, bucket, prefix) })
}
func (a *App) S3CreateFolder(id, bucket, prefix string) (bool, error) {
	return call(a, validate.First(target(id, bucket), validate.Prefix(prefix)), []string{id}, func() (bool, error) { return a.backend.S3.CreateFolder(a.backend.Context(), id, bucket, prefix) })
}
func (a *App) S3Move(id, bucket, from, to string) (int, error) {
	return call(a, validate.First(target(id, bucket), validate.Key(from), validate.Key(to)), []string{id}, func() (int, error) { return a.backend.S3.Move(a.backend.Context(), id, bucket, from, to) })
}
func (a *App) S3Presign(id, bucket, key string, expirySec int) (string, error) {
	return call(a, validate.First(target(id, bucket), validate.Key(key)), []string{id}, func() (string, error) { return a.backend.S3.Presign(a.backend.Context(), id, bucket, key, expirySec) })
}
func (a *App) S3SizeStart(id, bucket, prefix string, force bool) (bool, error) {
	return call(a, validate.First(target(id, bucket), validate.Prefix(prefix)), nil, func() (bool, error) { return a.backend.Sizer.Start(id, bucket, prefix, force) })
}
func (a *App) S3SizeCancel(id, bucket, prefix string) (bool, error) {
	return call(a, validate.First(target(id, bucket), validate.Prefix(prefix)), nil, func() (bool, error) { return a.backend.Sizer.Cancel(id, bucket, prefix) })
}
func (a *App) TransfersUpload(id, bucket, prefix string, paths []string) ([]transfers.Job, error) {
	return call(a, validate.First(target(id, bucket), validate.Prefix(prefix), validate.Strings(paths, 1000, func(s string) error { return validate.Text(s, "Đường dẫn tệp", 32767, false) })), []string{id}, func() ([]transfers.Job, error) { return a.backend.Transfers.Upload(id, bucket, prefix, paths) })
}
func (a *App) TransfersDownload(id, bucket string, keys []string, dir string) ([]transfers.Job, error) {
	return call(a, validate.First(target(id, bucket), validate.Strings(keys, 10000, validate.Key), validate.Text(dir, "Thư mục đích", 32767, false)), []string{id}, func() ([]transfers.Job, error) {
		if err := a.backend.CheckDirectory(dir); err != nil {
			return nil, err
		}
		return a.backend.Transfers.Download(id, bucket, keys, dir)
	})
}
func (a *App) TransfersCopy(srcID, srcBucket string, keys []string, dstID, dstBucket, prefix string) ([]transfers.Job, error) {
	return call(a, validate.First(target(srcID, srcBucket), target(dstID, dstBucket), validate.Strings(keys, 10000, validate.Key), validate.Prefix(prefix)), []string{srcID, dstID}, func() ([]transfers.Job, error) {
		return a.backend.Transfers.Copy(srcID, srcBucket, keys, dstID, dstBucket, prefix)
	})
}
func (a *App) TransfersCancel(id string) (bool, error) {
	return call(a, validate.ID(id), nil, func() (bool, error) { return a.backend.Transfers.Cancel(id) })
}
func (a *App) TransfersRetry(id string) (bool, error) {
	return call(a, validate.ID(id), nil, func() (bool, error) { return a.backend.Retry(id) })
}
func (a *App) TransfersList() ([]transfers.Job, error) {
	return call(a, nil, nil, func() ([]transfers.Job, error) { return a.backend.Transfers.List(), nil })
}
func (a *App) TransfersSetConcurrency(n int) (int, error) {
	return call(a, nil, nil, func() (int, error) { return a.backend.Transfers.SetConcurrency(n) })
}
func (a *App) runtimeContext() (context.Context, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.ctx == nil {
		return nil, apperr.New("RuntimeUnavailable", "Giao diện ứng dụng chưa sẵn sàng.")
	}
	return a.ctx, nil
}
func (a *App) DialogsUpload(directory bool) ([]string, error) {
	return call(a, nil, nil, func() ([]string, error) {
		ctx, err := a.runtimeContext()
		if err != nil {
			return nil, err
		}
		return dialogs.Upload(ctx, directory)
	})
}
func (a *App) DialogsDownloadDirectory() (string, error) {
	return call(a, nil, nil, func() (string, error) {
		ctx, err := a.runtimeContext()
		if err != nil {
			return "", err
		}
		path, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: "Chọn thư mục lưu tệp tải xuống", CanCreateDirectories: true})
		if err != nil || path == "" {
			return "", err
		}
		return a.backend.ChooseDirectory(path)
	})
}
func (a *App) ClipboardWriteText(text string) (bool, error) {
	return call(a, validate.Text(text, "Nội dung sao chép", 32768, false), nil, func() (bool, error) {
		ctx, err := a.runtimeContext()
		if err != nil {
			return false, err
		}
		err = runtime.ClipboardSetText(ctx, text)
		return err == nil, err
	})
}
