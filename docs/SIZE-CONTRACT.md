# Hợp đồng tính dung lượng bucket và thư mục (chỉ bản Lite)

Mục tiêu: tự tính và hiển thị dung lượng (tổng byte, số đối tượng) của bucket đang mở và của từng thư mục ảo đang hiển thị, không làm treo giao diện, hủy được, không tải dữ liệu đối tượng (chỉ ListObjectsV2).

## Phía Go (Codex phụ trách: app.go, internal/**, main.go nếu cần, *_test.go)

Gói mới `internal/sizer`, được `backend.Backend` giữ một thể hiện. Cách tính: duyệt ListObjectsV2 KHÔNG dùng delimiter với `Prefix = prefix`, cộng `Size` của mọi đối tượng, đếm số đối tượng. Dùng lại `s3svc.Service.Walk` nếu phù hợp. Đối tượng đánh dấu thư mục (khóa kết thúc bằng `/`, kích thước 0) vẫn tính vào số đối tượng, không ảnh hưởng byte.

Phương thức bind trên `*App` (Wails bind thành `window.go.main.App.<Tên>`):

- `S3SizeStart(id, bucket, prefix string, force bool) (bool, error)`: đưa khóa `(id, bucket, prefix)` vào hàng đợi tính. Trả về ngay (không chờ tính xong). `prefix` rỗng hoặc kết thúc bằng `/` (kiểm tra bằng `validate.Prefix`). Trả `true` nếu có việc được xếp hoặc đang chạy hoặc đã có kết quả trong cache (khi đó phát ngay một sự kiện `done` từ cache); `force=true` bỏ qua cache và tính lại. Khóa đang chạy mà gọi lại thì không chạy trùng.
- `S3SizeCancel(id, bucket, prefix string) (bool, error)`: hủy khóa đó nếu đang xếp hàng hoặc đang chạy (phát sự kiện `cancelled`); không có thì trả `false`, không lỗi.

Sự kiện đẩy về giao diện bằng `runtime.EventsEmit(ctx, "size:update", payload)`, payload JSON (camelCase):

```json
{ "profileId": "...", "bucket": "...", "prefix": "...", "state": "queued|running|done|error|cancelled", "bytes": 0, "objects": 0, "message": "" }
```

- `queued`: bytes, objects = 0. `running`: gửi số đang cộng dồn, không quá một sự kiện mỗi 300 ms cho mỗi khóa. `done`: kết quả cuối, được ghi vào cache. `error`: `message` là thông điệp tiếng Việt đã lọc secret qua `apperr.Filter` (không cache). `cancelled`: giữ số đã cộng dồn.
- Hàng đợi: tối đa 2 khóa chạy đồng thời toàn ứng dụng; còn lại xếp hàng theo thứ tự gọi.
- Cache trong bộ nhớ theo `(profileId, bucket, prefix)`, mất khi tắt ứng dụng. Hết hiệu lực (xóa mục cache của profile + bucket đó) khi có thao tác ghi thành công trên profile + bucket: Delete, DeletePrefix, CreateFolder, Move, và mọi tác vụ truyền tệp hoàn tất ở bucket đích. Khi hồ sơ bị sửa hoặc xóa (`Backend.Save`, `Backend.Remove`): hủy mọi việc đang chạy của profile đó và xóa cache của nó.
- Tắt ứng dụng (`shutdown`): hủy mọi việc đang chạy, không để goroutine rò rỉ.
- Không giữ khóa "profile bận" (`Backend.Begin`) trong suốt thời gian tính (có thể rất lâu); chỉ cần client S3 hợp lệ. Lỗi do client bị vô hiệu giữa chừng chuyển thành sự kiện `error` hoặc `cancelled`, không panic.
- Không được ghi secret hay accessKeyId vào sự kiện hoặc log.

## Phía giao diện (Gemini Argon phụ trách: frontend/dist/**, KHÔNG sửa Go)

`api-shim.js` thêm vào `window.api.s3`:

- `sizeStart(id, bucket, prefix, force)` gọi `S3SizeStart(id, bucket, prefix || '', Boolean(force))`.
- `sizeCancel(id, bucket, prefix)` gọi `S3SizeCancel(id, bucket, prefix || '')`.
- `onSize(callback)` đăng ký `runtime.EventsOn('size:update', callback)` và trả hàm hủy đăng ký (cùng kiểu với `transfers.onUpdate`).

Hành vi hiển thị (mô tả trong lời nhắc riêng của Argon).
