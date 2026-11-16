# AGENTS.md

## Quy tắc bắt buộc cho Swagger (`swag`)

Mỗi khi thêm mới hoặc chỉnh sửa bất kỳ HTTP handler / endpoint nào trong dự án:

1. **Bắt buộc thêm đầy đủ Swaggo annotations** ngay phía trên hàm handler:
   - `@Summary` — Mô tả ngắn gọn chức năng API
   - `@Tags` — Nhóm module (ví dụ: `Platform Auth`, `Platform IAM`, `Platform Organizations`, `Platform Catalog`, `Platform Billing`, `Platform Support`, `Platform Audit`, `Public Tenant`)
   - `@Accept json` (nếu có request body) và `@Produce json`
   - `@Security BearerAuth` (nếu endpoint yêu cầu xác thực JWT)
   - `@Param` cho toàn bộ `path`, `query`, `header` và `body` (ưu tiên trỏ tới struct Input/DTO cụ thể để Swagger sinh schema)
   - `@Success` và `@Failure` với mã HTTP tương ứng
   - `@Router /v1/... [method]` (khớp với `@BasePath /api` trong `main.go`)

2. **Đồng bộ tài liệu OpenAPI sau khi sửa handler/DTO**:
   - Chạy `swag init` (hoặc để `air` tự chạy theo `.air.toml`) để cập nhật `docs/docs.go`, `docs/swagger.json` và `docs/swagger.yaml`.
   - Kiểm tra `go build ./...` không có lỗi trước khi hoàn thành.
