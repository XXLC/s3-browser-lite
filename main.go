package main

import (
	"embed"
	"net/http"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// noStore tắt cache của WebView2: tài nguyên đã nhúng trong tệp thực thi, còn cache cũ
// (kể cả phản hồi 404) có thể làm giao diện của bản cập nhật không nạp đúng tệp mới.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:       "S3 Browser Lite",
		Width:       1280,
		Height:      800,
		AssetServer: &assetserver.Options{Assets: assets, Middleware: noStore},
		OnStartup:   app.startup,
		OnShutdown:  app.shutdown,
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true},
		Bind:        []interface{}{app},
	})
	if err != nil {
		println("Lỗi:", err.Error())
	}
}
