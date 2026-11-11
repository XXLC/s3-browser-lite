# Hợp đồng bản web (S3 Browser Lite Web)

Bản web là một tệp thực thi Go duy nhất (`cmd/s3browser-web`), chạy trên Windows, Linux, macOS (amd64 và arm64), phục vụ cùng giao diện `frontend/dist/` qua HTTP. Không phụ thuộc Wails, không cgo, nên build chéo được từ một máy. Nghiệp vụ dùng chung với bản desktop qua gói `internal/service`.

## Quyết định thiết kế (đã chốt)

- Mặc định chỉ nghe `127.0.0.1:8080`. Nghe địa chỉ khác (`-listen 0.0.0.0:8080`) được phép, kèm cảnh báo nên dùng TLS (`-tls-cert`, `-tls-key`) hoặc reverse proxy.
- Luôn bắt xác thực bằng token. Không có chế độ tắt xác thực.
- Tải lên: trình duyệt gửi tệp lên máy chủ (multipart), máy chủ lưu tạm rồi đẩy vào hàng đợi truyền tệp sẵn có (giữ nguyên hủy, thử lại, đồng thời, tiến độ). Tải xuống: máy chủ phát luồng trực tiếp từ S3 về trình duyệt (một tệp thì gửi nguyên tệp, nhiều tệp hoặc thư mục thì nén zip không nén, `zip.Store`), không qua hàng đợi.
- Hồ sơ lưu cùng tệp `profiles.json` như bản desktop (`os.UserConfigDir()/S3BrowserLite`). Windows dùng DPAPI, macOS dùng Keychain, Linux dùng khóa AES-GCM (xem mục Linux).

## Tham số dòng lệnh

| Cờ | Mặc định | Ý nghĩa |
| --- | --- | --- |
| `-listen` | `127.0.0.1:8080` | Địa chỉ nghe |
| `-token` | rỗng | Token cố định. Rỗng thì sinh ngẫu nhiên 32 byte (base64url) mỗi lần chạy. Token tự đặt phải dài ít nhất 16 ký tự. Cũng đọc từ biến môi trường `S3BROWSER_WEB_TOKEN` (cờ thắng) |
| `-tls-cert`, `-tls-key` | rỗng | Bật HTTPS khi có cả hai |

Khi khởi động in ra (tiếng Việt, không in token vào tệp hay log nào khác): địa chỉ nghe và một dòng `Mở: http://127.0.0.1:8080/?token=<token>` (đúng scheme). Tắt bằng SIGINT/SIGTERM: đóng backend, dọn thư mục lưu tạm.

## Xác thực và an toàn

- Cookie `s3b_session` = token, `HttpOnly`, `SameSite=Strict`, `Path=/`, `Secure` khi chạy TLS. So sánh hằng thời gian (`crypto/subtle`).
- `GET /?token=<t>` đúng thì đặt cookie rồi 302 về `/` (bỏ token khỏi URL). Sai thì hiện trang đăng nhập.
- Chưa có cookie hợp lệ: mọi đường dẫn không phải API trả trang đăng nhập (HTML tối giản tự chứa, tiếng Việt, một ô nhập token, `POST /login` trường `token`, đúng thì đặt cookie và 303 về `/`, sai thì hiện lại trang kèm lỗi, chờ 500 ms). Mọi đường dẫn `/api/` chưa xác thực trả 401 JSON `{"code":"Unauthorized","message":"..."}`.
- Chống DNS rebinding: khi nghe địa chỉ loopback, `Host` phải là `localhost`, `127.0.0.1` hoặc `[::1]` (kèm cổng đang nghe), sai thì 403.
- Chống CSRF: mọi yêu cầu `POST` có `Origin` thì `Origin` phải khớp `scheme://Host`, sai thì 403. Cookie `SameSite=Strict` là lớp thứ hai.
- `POST /api/rpc/*` bắt buộc `Content-Type: application/json`, thân tối đa 4 MiB.
- Tiêu đề mọi phản hồi: `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin` (không dùng `no-referrer`: trình duyệt khi đó gửi `Origin: null` cho form tải xuống và bị chặn bởi kiểm tra Origin), `Cache-Control: no-store`. HSTS không đặt.
- Máy chủ `http.Server` có `ReadHeaderTimeout` 10 s; không đặt `WriteTimeout` (có luồng dài).
- Bí mật (`secretAccessKey`, token) không bao giờ vào log hay phản hồi. Quy tắc lọc lỗi của `apperr.Filter` giữ nguyên.

## Điểm cuối

Tất cả cùng nguồn gốc (same-origin). Mọi phản hồi lỗi có dạng JSON `{"code":"<Mã>","message":"<Thông điệp tiếng Việt>"}` (dạng `apperr.Error`).

### Giao diện tĩnh

- `GET /` và `GET /index.html`: nội dung `frontend/dist/index.html` đã đổi ở phía máy chủ: thay `src="./api-shim.js"` bằng `src="./api-web.js"`, và trong thẻ CSP đổi `form-action 'none'` thành `form-action 'self'`, và `frame-src 'none'` thành `frame-src 'self'` (iframe ẩn nhận tải xuống). Nếu không tìm thấy chuỗi cần thay thì máy chủ không khởi động (lỗi rõ ràng). Có kiểm thử cho việc này.
- Các tệp còn lại của `frontend/dist/` phục vụ nguyên trạng (`/app.js`, `/styles.css`, `/i18n.js`, `/api-web.js`). `/api-shim.js` không phục vụ (404) để tránh nhầm.

### RPC: `POST /api/rpc/<Method>`

- `<Method>` là tên phương thức công khai của `service.Service` (cùng tên với bản Wails: `ProfilesList`, `S3List`, `TransfersCancel`...). Phương thức không tồn tại: 404 `MethodNotFound`.
- Thân: mảng JSON các tham số theo đúng thứ tự (mảng rỗng `[]` nếu không có tham số). Số lượng sai hoặc kiểu sai: 400 `InvalidArguments`.
- Thành công: 200, thân `{"result": <giá trị>}`.
- Lỗi nghiệp vụ: 400 với thân là chính `apperr.Error` (mã gốc, ví dụ `NoSuchBucket`), KHÔNG bọc thêm.
- Triển khai bằng phản chiếu (`reflect`) trên `*service.Service`, không viết tay từng phương thức (DRY với bản desktop).
- `ClipboardWriteText`, `DialogsUpload`, `DialogsDownloadDirectory` KHÔNG nằm trong `service.Service` (chỉ có ở bản desktop), nên không gọi được qua RPC.

### Sự kiện: `GET /api/events` (Server-Sent Events)

- `Content-Type: text/event-stream`. Mỗi sự kiện: `event: transfers:update` hoặc `event: size:update`, `data: <JSON một dòng>` (cùng dạng `transfers.Job` và `sizer.Event` như bản Wails).
- Dòng chú thích `: ping` mỗi 20 giây để giữ kết nối. Gửi tới mọi trình duyệt đang nối; bộ đệm mỗi người nghe giới hạn, người nghe chậm bị bỏ gói thay vì chặn backend.
- Không phát lại sự kiện cũ khi nối lại; giao diện tự gọi `TransfersList` sau khi nối lại.

### Tải lên: `POST /api/upload?profile=<id>&bucket=<tên>&prefix=<tiền tố>`

- `Content-Type: multipart/form-data`. `prefix` rỗng hoặc kết thúc bằng `/` (kiểm bằng `validate.Prefix`).
- Mỗi tệp gồm CẶP phần, theo đúng thứ tự: trường `path` (đường dẫn tương đối dùng `/`, ví dụ `a.txt` hoặc `thư-mục/con/a.txt`; lấy từ `webkitRelativePath` khi chọn thư mục) rồi phần tệp `file`. (Không dùng tên tệp trong `Content-Disposition` vì `mime/multipart` đã cắt về tên cuối.)
- Máy chủ đọc theo luồng (`MultipartReader`), không nạp cả tệp vào bộ nhớ. Chống đi ra ngoài: từ chối `path` rỗng, tuyệt đối, chứa `..`, `.`, ký tự điều khiển, `\`, hoặc phần tử rỗng; tối đa 10000 tệp mỗi yêu cầu. Lỗi thì xóa phần đã lưu của yêu cầu và trả lỗi.
- Lưu tạm vào `<thư mục lưu tạm của tiến trình>/<mã lô>/<số thứ tự phần tử gốc>/<phần tử gốc>/...`. Phần tử gốc là đoạn đầu của `path` (tệp đơn hoặc thư mục gốc). Mỗi phần tử gốc đưa vào `Transfers.Upload(profile, bucket, prefix, []string{<đường dẫn lưu tạm của phần tử gốc>})` nên mỗi phần tử gốc là một job, tên job là tên phần tử gốc.
- Dọn dẹp: khi job của phần tử gốc chuyển `completed` thì xóa thư mục lưu tạm của nó. `failed` hoặc `cancelled` thì giữ lại để thử lại; thư mục tạm của tiến trình xóa hết khi tắt.
- Thành công: 200 `{"result": [Job, ...]}` (các job vừa xếp hàng).
- Thân yêu cầu không giới hạn kích thước. Hủy kết nối giữa chừng thì xóa phần đã lưu và không tạo job.
- Phải qua `backend.Begin(profileID)` giống `call` ở RPC, kiểm `validate.ID`, `validate.Bucket`, `validate.Prefix`.

### Tải xuống: `GET /api/download?profile=<id>&bucket=<tên>&key=<khóa>` và `POST /api/download`

- `GET`: một khóa không kết thúc bằng `/`. Phát luồng trực tiếp từ `s3svc.Service.Get`, kèm `Content-Disposition: attachment; filename="<tên ASCII an toàn>"; filename*=UTF-8''<tên>`, `Content-Type` từ S3 (mặc định `application/octet-stream`), `Content-Length` khi biết.
- `POST` (`application/x-www-form-urlencoded`, dùng cho form ẩn): trường `profile`, `bucket`, `keys` (chuỗi JSON mảng khóa, 1 đến 10000 khóa). Một khóa tệp: như `GET`. Ngược lại: zip phát luồng (`archive/zip`, `zip.Store`), tên `<bucket>.zip` (một thư mục duy nhất thì `<tên thư mục>.zip`), mỗi mục có tên là chính khóa đối tượng (giống bản desktop giữ nguyên khóa); khóa kết thúc `/` được mở bằng `Walk`; bỏ qua khóa trùng; từ chối khóa chứa phần tử `..` hoặc bắt đầu bằng `/`. Không kiểm soát được lỗi sau khi đã gửi tiêu đề; lỗi giữa luồng thì đóng kết nối (trình duyệt báo tải lỗi).
- Lỗi trước khi gửi tiêu đề: JSON lỗi với mã trạng thái phù hợp (400 hoặc 404).
- Hủy khi trình duyệt ngắt kết nối (dùng `r.Context()`).

## Linux (khóa mã hóa hồ sơ)

`internal/profiles/oscipher_other.go` (build tag `!windows && !darwin`) dùng AES-256-GCM với khóa chủ:

1. Biến môi trường `S3BROWSER_MASTER_KEY` (base64 chuẩn của đúng 32 byte) nếu có; sai định dạng thì lỗi rõ ràng, không lưu.
2. Ngược lại tệp `os.UserConfigDir()/S3BrowserLite/profiles.key` (32 byte ngẫu nhiên, quyền `0600`, thư mục `0700`), tạo khi cần mã hóa lần đầu; chỉ đọc thì không tạo.

Tái dùng `aesCipher` có sẵn trong `internal/profiles`. README phải nói thẳng: khóa nằm cạnh dữ liệu nên chỉ chống lộ vô tình (sao lưu, chép tệp), không chống kẻ đã đọc được thư mục cấu hình; muốn mạnh hơn thì đặt khóa qua biến môi trường từ kho bí mật.

## Giao diện (frontend) cho bản web

`frontend/dist/api-web.js` thay `api-shim.js` ở bản web và dựng lại ĐÚNG đối tượng `window.api` (cùng hình dạng như `api-shim.js`), dùng `fetch` và `EventSource`. Khác biệt có chủ đích:

- `api.dialogs.upload(directory)`: mở hộp chọn tệp của trình duyệt (`<input type="file" multiple>`, hoặc `webkitdirectory` khi `directory` đúng) và trả mảng mục web `{ file: File, path: string }` (rỗng nếu hủy). `path` là `webkitRelativePath` hoặc `file.name`.
- `api.transfers.upload(id, bucket, prefix, items)`: nhận mục web ở trên, gửi `POST /api/upload` bằng `XMLHttpRequest` để có tiến độ gửi lên máy chủ. Trong lúc gửi, phát tới các hàm đăng ký `onUpdate` một job giả `{id: "web-upload-<n>", type: "upload", name, status: "running", transferred, total, speed, error: null}` (cập nhật theo tiến độ). Gửi xong thì phát `{...job, removed: true}` (giao diện xóa job giả; job thật tới qua SSE). Gửi lỗi thì job giả `status: "failed"` kèm `error` (không `removed`). `api.transfers.cancel("web-upload-<n>")` hủy XHR (job giả thành `cancelled`); `retry` job giả không hỗ trợ (báo lỗi rõ ràng, người dùng chọn lại tệp). Các id khác đi RPC bình thường.
- `api.files.onDrop(callback)`: lắng nghe `drop` trên `document`; đọc `DataTransferItem.webkitGetAsEntry()` NGAY trong sự kiện (đồng bộ), duyệt đệ quy thư mục, gọi `callback(items)` với mục web (đường dẫn tương đối dùng `/`). Trả hàm hủy đăng ký.
- `api.dialogs.downloadDirectory()`: trả chuỗi `"browser"` (không có hộp thoại thư mục). `api.transfers.download(id, bucket, keys, directory)`: gửi form ẩn `POST /api/download` vào iframe ẩn rồi trả mảng rỗng.
- `api.clipboard.writeText(value)`: `navigator.clipboard.writeText` khi có, nếu không (HTTP không an toàn) dùng `<textarea>` ẩn và `document.execCommand("copy")`; lỗi thì ném Error có `code`.
- `api.s3.onSize`, `api.transfers.onUpdate`: đăng ký vào kết nối `EventSource("/api/events")` dùng chung (mở khi có người đăng ký đầu tiên); khi nối lại sau lỗi thì gọi `TransfersList` và phát lại từng job qua `onUpdate`.
- Lỗi: phản hồi không 2xx có JSON `{code, message}` thì ném `Error` có `code` và `message` đó (cùng hình dạng `createError` của `api-shim.js`). Mất kết nối mạng: mã `NetworkError`. 401: tải lại trang (hiện trang đăng nhập).
- Sửa nhỏ trong `app.js` (bản desktop không đổi hành vi):
  - Hàm `download()`: chỉ báo `t.queuedTransfer` khi mảng job trả về có phần tử; nếu rỗng thì báo thông điệp mới `t.downloadStarted` ("Đã bắt đầu tải xuống qua trình duyệt.") thêm vào `i18n.js` ở mọi ngôn ngữ có sẵn.
  - Nhận job có `removed: true` ở `api.transfers.onUpdate` thì `state.tasks.delete(job.id)` thay vì ghi đè.
