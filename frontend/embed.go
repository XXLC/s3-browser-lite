package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var raw embed.FS

// FS xuất hệ thống tệp giao diện web đã nhúng trong thư mục dist.
var FS, _ = fs.Sub(raw, "dist")
