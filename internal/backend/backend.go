package backend

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"s3browser/internal/apperr"
	"s3browser/internal/profiles"
	"s3browser/internal/s3svc"
	"s3browser/internal/sizer"
	"s3browser/internal/transfers"
)

// Backend giữ khóa nghiệp vụ chung giữa thao tác S3, hàng đợi và sửa hồ sơ.
type Backend struct {
	mu          sync.Mutex
	Profiles    *profiles.Store
	S3          *s3svc.Service
	Transfers   *transfers.Transfers
	Sizer       *sizer.Sizer
	Filter      *apperr.Filter
	Err         error
	active      map[string]int
	directories map[string]bool
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
}

func New(store *profiles.Store, update func(transfers.Job), sizeUpdate ...func(sizer.Event)) *Backend {
	filter := &apperr.Filter{}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Backend{Profiles: store, Filter: filter, active: map[string]int{}, directories: map[string]bool{}, ctx: ctx, cancel: cancel}
	b.S3 = s3svc.New(store, filter)
	var onSize func(sizer.Event)
	if len(sizeUpdate) > 0 {
		onSize = sizeUpdate[0]
	}
	b.Sizer = sizer.New(b.S3, filter, onSize)
	b.S3.OnWrite = b.Sizer.InvalidateBucket
	b.Transfers = transfers.New(b.S3, filter, update)
	b.Transfers.OnWrite = b.Sizer.InvalidateBucket
	return b
}
func Default(update func(transfers.Job), sizeUpdate ...func(sizer.Event)) *Backend {
	store, err := profiles.Default()
	b := New(store, update, sizeUpdate...)
	b.Err = err
	return b
}
func (b *Backend) Context() context.Context { return b.ctx }
func (b *Backend) Begin(ids ...string) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, apperr.New("AppClosed", "Ứng dụng đã đóng.")
	}
	for _, id := range ids {
		b.active[id]++
	}
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, id := range ids {
			b.active[id]--
			if b.active[id] == 0 {
				delete(b.active, id)
			}
		}
	}, nil
}
func (b *Backend) mutable(id string) error {
	if b.closed {
		return apperr.New("AppClosed", "Ứng dụng đã đóng.")
	}
	if id != "" && (b.active[id] > 0 || b.Transfers.UsesProfile(id)) {
		return apperr.New("ProfileBusy", "Hồ sơ đang được dùng cho thao tác S3 hoặc truyền tệp. Hãy chờ hoàn thành hoặc hủy tác vụ trước khi sửa hoặc xóa.")
	}
	return nil
}
func (b *Backend) Save(p profiles.Profile) (profiles.Profile, error) {
	b.Filter.Remember(p.AccessKeyID, p.SecretAccessKey)
	b.mu.Lock()
	if err := b.mutable(p.ID); err != nil {
		b.mu.Unlock()
		return profiles.Profile{}, err
	}
	saved, err := b.Profiles.Save(p)
	if err == nil {
		b.S3.Invalidate(saved.ID)
	}
	b.mu.Unlock()
	if err == nil {
		b.Sizer.InvalidateProfile(saved.ID)
	}
	return saved, err
}
func (b *Backend) Remove(id string) (bool, error) {
	b.mu.Lock()
	if err := b.mutable(id); err != nil {
		b.mu.Unlock()
		return false, err
	}
	ok, err := b.Profiles.Remove(id)
	if err == nil {
		b.S3.Invalidate(id)
	}
	b.mu.Unlock()
	if err == nil {
		b.Sizer.InvalidateProfile(id)
	}
	return ok, err
}
func (b *Backend) Retry(id string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Transfers.Retry(id)
}
func (b *Backend) ChooseDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", apperr.New("ForbiddenPath", "Không xác định được thư mục đã chọn.")
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", apperr.New("ForbiddenPath", "Không truy cập được thư mục đã chọn.")
	}
	stat, err := os.Stat(real)
	if err != nil || !stat.IsDir() {
		return "", apperr.New("ForbiddenPath", "Đường dẫn đã chọn không phải thư mục.")
	}
	b.mu.Lock()
	b.directories[real] = true
	b.mu.Unlock()
	return real, nil
}
func (b *Backend) CheckDirectory(path string) error {
	b.mu.Lock()
	allowed := b.directories[path]
	b.mu.Unlock()
	real, err := filepath.EvalSymlinks(path)
	if !allowed || err != nil || real != path {
		return apperr.New("ForbiddenPath", "Hãy chọn thư mục đích qua hộp thoại của ứng dụng.")
	}
	return nil
}
func (b *Backend) Close() {
	b.mu.Lock()
	b.closed = true
	b.directories = map[string]bool{}
	b.cancel()
	b.mu.Unlock()
	b.Sizer.Close()
	b.Transfers.Close()
	b.S3.Close()
}
