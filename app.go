package main

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"s3browser/internal/apperr"
	"s3browser/internal/backend"
	"s3browser/internal/dialogs"
	"s3browser/internal/service"
	"s3browser/internal/sizer"
	"s3browser/internal/transfers"
	"s3browser/internal/validate"
)

type App struct {
	*service.Service
	mu  sync.RWMutex
	ctx context.Context
}

func NewApp() *App {
	a := &App{}
	b := backend.Default(func(job transfers.Job) {
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
	a.Service = service.New(b)
	return a
}

func (a *App) startup(ctx context.Context) { a.mu.Lock(); a.ctx = ctx; a.mu.Unlock() }
func (a *App) shutdown(context.Context)    { a.Backend.Close(); a.mu.Lock(); a.ctx = nil; a.mu.Unlock() }

func (a *App) runtimeContext() (context.Context, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.ctx == nil {
		return nil, apperr.New("RuntimeUnavailable", "Giao diện ứng dụng chưa sẵn sàng.")
	}
	return a.ctx, nil
}

func (a *App) DialogsUpload(directory bool) ([]string, error) {
	return service.Call(a.Service, nil, nil, func() ([]string, error) {
		ctx, err := a.runtimeContext()
		if err != nil {
			return nil, err
		}
		return dialogs.Upload(ctx, directory)
	})
}

func (a *App) DialogsDownloadDirectory() (string, error) {
	return service.Call(a.Service, nil, nil, func() (string, error) {
		ctx, err := a.runtimeContext()
		if err != nil {
			return "", err
		}
		path, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: "Chọn thư mục lưu tệp tải xuống", CanCreateDirectories: true})
		if err != nil || path == "" {
			return "", err
		}
		return a.Backend.ChooseDirectory(path)
	})
}

func (a *App) ClipboardWriteText(text string) (bool, error) {
	return service.Call(a.Service, validate.Text(text, "Nội dung sao chép", 32768, false), nil, func() (bool, error) {
		ctx, err := a.runtimeContext()
		if err != nil {
			return false, err
		}
		err = runtime.ClipboardSetText(ctx, text)
		return err == nil, err
	})
}
