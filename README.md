# MemRemark
Ứng dụng duy trì ngữ cảnh liền mạch giữa các phiên làm việc bằng cách tự động ghi lại các quan sát về việc sử dụng công cụ, tạo ra các bản tóm tắt ngữ nghĩa và cung cấp chúng cho các phiên làm việc sau này. Điều này cho phép Antigravity CLI / Claude Code duy trì tính liên tục của kiến thức về các dự án ngay cả sau khi các phiên làm việc kết thúc hoặc kết nối lại.

## Tech stack
- Golang cho hiệu năng cao
- Lưu trữ: Sqlite (đã chốt cho Core Engine v1, xem spec). Cân nhắc PostgreSQL/MySQL/vector database sau, nếu Sync Layer hoặc semantic search thực sự cần.

## Ý tưởng
Tham khảo từ 2 repo này để xây dựng dự án: [Claude Mem](https://github.com/thedotmack/claude-mem), [Mempalace](https://github.com/mempalace/mempalace)

## Hiện trạng
Hiện tại có tới tận 3 thiết bị sử dụng CLI (Claude Code, Antigravity CLI), chủ yếu là [Antigravity CLI](https://antigravity.google/product/antigravity-cli) là PC1 (trên công ty), PC2 (tại nhà), Laptop (tại nhà).
Mỗi lần cần thảo luận hay gì thi phải nói lại toàn bộ lịch sử trò chuyện, cho nên là cực kỳ bất tiện.

## Mục tiêu
**Tự động** lưu trữ, trích xuất và duy trì liền mạch cuộc trò chuyện và kiến thức được đúc kết lại qua toàn bộ các thiết bị mà không bị ngắt quãng.

## Tiến độ
Chia làm 2 phần: **Core Engine** (trên 1 máy) làm trước, **Sync Layer** (đồng bộ đa thiết bị) làm sau.
**Core Engine đã cài đặt xong và qua review đầy đủ** (11 task theo kế hoạch + 1 vòng final review chia 4 mảng, tìm và sửa 5 lỗi Critical + 6 lỗi Important). Xem thiết kế tại `docs/superpowers/specs/2026-08-10-core-engine-design.md` (bản tiếng Việt: `2026-08-10-core-engine-design-vi.md`) và kế hoạch triển khai tại `docs/superpowers/plans/2026-08-10-core-engine-implementation.md`. Sync Layer chưa bắt đầu.

## Build

Yêu cầu Go 1.22+.

```bash
go build -o memremarkd ./cmd/memremarkd
go build -o memremark-hook-claude-sessionstart ./cmd/memremark-hook-claude-sessionstart
```

Kiểm tra: `go test ./...` (hoặc thêm `-race` để chắc chắn hơn).

## Sử dụng

### 1. Chạy daemon

```bash
./memremarkd
```

Daemon chạy nền, poll mỗi 3 giây, đọc trực tiếp:
- Transcript Claude Code tại `~/.claude/projects/`
- Database hội thoại của Antigravity CLI tại `~/.gemini/antigravity-cli/`

và ghi kết quả vào `~/.memremark/memremark.db` (tự tạo thư mục/file nếu chưa có, quyền `0700`/`0600` vì đây là log hoạt động riêng tư).

Dừng bằng Ctrl+C (hoặc gửi `SIGTERM`) — daemon tắt sạch sẽ. **Lưu ý**: daemon hiện chưa có cơ chế tự khởi động lại khi crash hay tự chạy cùng lúc mở CLI — cần tự chạy tay hoặc tự cấu hình bằng systemd/launchd nếu muốn nó luôn chạy nền.

### 2. Cài hook nạp lại ngữ cảnh cho Claude Code

Binary `memremark-hook-claude-sessionstart` đọc bản tóm tắt gần nhất từ `~/.memremark/memremark.db` cho project hiện tại và in ra JSON theo đúng chuẩn hook `SessionStart` của Claude Code.

Thêm vào `~/.claude/settings.json` (hoặc `.claude/settings.json` trong từng project nếu chỉ muốn bật cho project đó):

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|clear|compact",
        "hooks": [
          {
            "type": "command",
            "command": "/duong/dan/toi/memremark-hook-claude-sessionstart"
          }
        ]
      }
    ]
  }
}
```

Thay `/duong/dan/toi/` bằng đường dẫn thật tới binary đã build ở bước Build.

### 3. Antigravity CLI

Hiện **chưa có** cơ chế nạp lại ngữ cảnh cho Antigravity CLI (daemon vẫn đọc và tóm tắt dữ liệu của nó bình thường, chỉ là chưa tiêm được ngược lại vào session mới) — xem mục "Giới hạn hiện tại" bên dưới.

## Giới hạn hiện tại

- Daemon không tự khởi động lại khi crash và không tự chạy khi mở CLI — phải tự quản lý vòng đời tiến trình.
- Chưa có cơ chế nạp ngữ cảnh cho Antigravity CLI (do bản thân Antigravity CLI chưa có hook nào thực thi được trong môi trường đã kiểm chứng — xem spec §2.1, §10).
- Việc trích xuất nội dung từ Antigravity CLI (`internal/adapter/antigravity`) dùng phương pháp heuristic quét chuỗi trong dữ liệu protobuf thô (không có schema chính thức) — đủ dùng nhưng không đảm bảo trích xuất được cấu trúc tool/argument riêng biệt, chỉ có nội dung text thô.
- Chưa có Sync Layer — dữ liệu chỉ nằm trên từng máy riêng lẻ.

