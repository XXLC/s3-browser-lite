# S3 Browser Lite

S3 Browser Lite là ứng dụng máy tính để bàn giúp quản lý kho lưu trữ AWS S3 và các dịch vụ S3 tương thích (MinIO, Ceph RGW...). Dự án được chuyển đổi từ bản Electron sang kiến trúc Go và Wails v2, giữ nguyên toàn bộ giao diện, tính năng và các bất biến bảo mật của bản gốc nhưng không đóng gói kèm nhân Chromium và Node.js.

## So sánh với bản Electron (kích thước và tài nguyên)

Bản này dùng WebView2 có sẵn trên Windows thay vì đóng gói kèm Chromium và Node.js, nên tệp phân phối nhỏ hơn nhiều. Lợi ích về bộ nhớ chạy thì không có (xem số đo bên dưới): WebView2 vẫn chạy nhiều tiến trình nên RAM tương đương bản Electron.

### Số đo (một lần đo, Windows 11, sau khoảng 8 giây khởi động, chưa mở hồ sơ nào)

| Tiêu chí | Bản Electron | Bản S3 Browser Lite (Go + Wails) |
| --- | --- | --- |
| Tệp phân phối | 105,5 MB (portable) | 17,7 MB (`S3BrowserLite.exe`) |
| Số tiến trình | 4 | 7 (gồm các tiến trình WebView2) |
| Working set của cả cây tiến trình | khoảng 309 MB | khoảng 338 MB |
| Private memory của cả cây tiến trình | khoảng 165 MB | khoảng 212 MB |
| Thời gian khởi động lạnh | chưa đo | chưa đo |

Working set tính trùng các trang bộ nhớ dùng chung giữa các tiến trình, nên số đo chỉ mang tính tham khảo.

## Tính năng chính

- Quản lý hồ sơ kết nối: thêm, sửa, xóa, nhân bản, kiểm tra kết nối, nhập và xuất danh sách hồ sơ định dạng JSON.
- Hỗ trợ S3 tương thích: cấu hình endpoint tùy chỉnh, vùng (region), Path-Style hoặc Virtual-Hosted-Style, tự động tương thích với MinIO cũ, Ceph RGW và ECS nhờ chế độ tính checksum khi bắt buộc (`WhenRequired`).
- Duyệt đa tab: mở nhiều tab làm việc đồng thời trên các hồ sơ và bucket khác nhau.
- Quản lý bucket và đối tượng: liệt kê và tạo bucket (kiểm tra quy tắc tên 3-63 ký tự và `LocationConstraint`), duyệt thư mục phân trang theo token, xem thông tin chi tiết (head metadata), tạo thư mục, đổi tên, di chuyển, xóa lô tối đa 1000 khóa và xóa theo tiền tố.
- Truyền tệp tin: tải lên (multipart tự động, hỗ trợ chọn tệp/thư mục hoặc kéo thả trực tiếp vào giao diện), tải xuống an toàn qua tệp tạm, sao chép đối tượng trực tiếp giữa các hồ sơ/endpoint khác nhau, điều chỉnh độ đồng thời, hủy và thử lại tác vụ trong hàng đợi.
- Tạo liên kết tạm thời (Presigned URL) và sao chép nhanh vào clipboard.

## Yêu cầu hệ thống

- Hệ điều hành: Windows 10 hoặc Windows 11 (x64), hoặc macOS (bản universal, build qua GitHub Actions; chưa ký và chưa notarize nên lần đầu mở phải chuột phải chọn Open).
- Microsoft Edge WebView2 Runtime (đã cài sẵn trên Windows 10 bản mới và Windows 11).

## Cài đặt công cụ phát triển (Toolchain)

1. Cài đặt Go 1.27 (x64 cho Windows).
2. Cài đặt Wails CLI v2 (phiên bản dự kiến v2.17, cần xác minh phiên bản phát hành trong môi trường của bạn):

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

Lưu ý: Nếu làm việc trong môi trường không có mạng, cần đảm bảo tệp thực thi `wails.exe` đã có sẵn trong `PATH` (ví dụ `C:\Users\<tên>\go\bin`) và sử dụng các gói đã được vendor sẵn trong thư mục `vendor/`.

## Build ứng dụng

Mọi phụ thuộc Go đã nằm trong `vendor/`, giao diện là HTML/CSS/JS thuần trong `frontend/dist/` nên không cần Node.js hay npm.

Thiết lập biến môi trường (nếu build ngoại tuyến trong thư mục dự án) và chạy lệnh build:

```powershell
$env:GOFLAGS = "-mod=vendor"
$env:GOCACHE = "$PWD\.gocache"
wails build -s
```

Sau khi hoàn tất, tệp thực thi độc lập được tạo tại:

```text
build/bin/S3BrowserLite.exe
```

## Chạy thử

Dự án không có bước biên dịch hay đóng gói frontend (không dùng bundler), vì vậy không cần chạy `wails dev`. Để chạy thử, hãy build ứng dụng rồi chạy trực tiếp tệp thực thi:

```powershell
wails build -s
.\build\bin\S3BrowserLite.exe
```

## Kiểm thử

Toàn bộ kiểm thử đơn vị và tích hợp sử dụng giả lập nội bộ (interface hoặc `net/http/httptest`), không gọi ra mạng thật:

```powershell
$env:GOFLAGS = "-mod=vendor"
$env:GOCACHE = "$PWD\.gocache"
go vet ./...
go test ./...
```

## Cấu trúc thư mục

```text
main.go                      Khởi tạo Wails, bind App, bật kéo thả tệp (EnableFileDrop)
app.go                       Các phương thức public gắn với window.go.main.App
internal/profiles/           Lưu trữ hồ sơ, mã hóa khóa bí mật bằng Windows DPAPI hoặc macOS Keychain
internal/s3svc/              Tạo và cache S3 client theo hồ sơ, thực thi nghiệp vụ S3
internal/transfers/          Hàng đợi tải lên, tải xuống, sao chép và phát sự kiện tiến độ
internal/apperr/             Chuẩn hóa mã lỗi và thông điệp tiếng Việt trả về giao diện
frontend/dist/               Giao diện HTML/CSS/JS thuần (index.html, app.js, styles.css, i18n.js, api-shim.js)
reference/electron/          Mã nguồn và đặc tả bản Electron dùng để đối chiếu (chỉ đọc)
vendor/                      Các gói phụ thuộc Go đã vendor
```

## Bảo mật

- Bảo vệ khóa bí mật bằng DPAPI (Windows) hoặc Keychain (macOS, AES-256-GCM với khóa chủ nằm trong Keychain đăng nhập, mục `S3BrowserLite`/`profiles-key`, hồ sơ lưu ở `~/Library/Application Support/S3BrowserLite/profiles.json`). Phần sau mô tả bản Windows: `secretAccessKey` được mã hóa bằng Windows DPAPI (`CryptProtectData`, phạm vi người dùng hiện tại) trước khi lưu dạng base64 vào `%APPDATA%\S3BrowserLite\profiles.json`. Nếu DPAPI gặp lỗi, ứng dụng từ chối lưu và báo lỗi rõ ràng, tuyệt đối không lưu dạng văn bản rõ.
- Không rò rỉ bí mật: Tầng giao diện (renderer) không bao giờ nhận `secretAccessKey` mà chỉ nhận cờ `hasSecret`. Khi xuất hồ sơ ra JSON hoặc nhập từ JSON, khóa bí mật luôn bị loại bỏ. Mọi thông báo lỗi và nhật ký đều lọc bỏ `accessKeyId` và `secretAccessKey` (kể cả dạng đã mã hóa URL).
- Cô lập thông tin xác thực: `aws.Config` được dựng thủ công với khóa tĩnh của từng hồ sơ, không tự động đọc biến môi trường, tệp `~/.aws` hay IMDS.
- Cô lập cấu hình TLS lỏng: Tùy chọn `allowInsecureTls` chỉ gắn `InsecureSkipVerify` vào `http.Client` riêng của đúng hồ sơ đó, không làm thay đổi `http.DefaultTransport` của toàn ứng dụng.
- An toàn đường dẫn tải xuống: Kiểm tra chống leo thang thư mục (path traversal) và chỉ cho phép ghi tệp vào đúng thư mục mà người dùng đã chọn thông qua hộp thoại hệ thống của ứng dụng.

## Giấy phép

Dự án được phát hành theo giấy phép MIT (cần xác minh tệp `LICENSE` ở thư mục gốc của kho mã nguồn).
