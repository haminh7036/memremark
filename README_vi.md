# MemRemark

[English](README.md) | [Tiếng Việt](README_vi.md)

Ứng dụng duy trì ngữ cảnh liền mạch giữa các phiên làm việc bằng cách tự động ghi lại các quan sát về việc sử dụng công cụ, tạo ra các bản tóm tắt ngữ nghĩa và cung cấp chúng cho các phiên làm việc sau này. Điều này cho phép Antigravity CLI / Claude Code duy trì tính liên tục của kiến thức về các dự án ngay cả sau khi các phiên làm việc kết thúc hoặc kết nối lại.

## Tech stack
- Golang cho hiệu năng cao
- Lưu trữ: Sqlite (đã chốt cho Core Engine v1, xem spec). Cân nhắc PostgreSQL/MySQL/vector database sau, nếu Sync Layer hoặc semantic search thực sự cần.

## Ý tưởng
Tham khảo từ 2 repo này để xây dựng dự án: [Claude Mem](https://github.com/thedotmack/claude-mem), [Mempalace](https://github.com/mempalace/mempalace)

## Hiện trạng
Hiện tại tôi sử dụng CLI (Claude Code, Antigravity CLI) trên 3 thiết bị: PC1 (trên công ty), PC2 (tại nhà) và Laptop (tại nhà).
Mỗi lần chuyển đổi thiết bị hoặc mở phiên làm việc mới phải giải thích lại toàn bộ ngữ cảnh, quy ước và lịch sử trước đó, gây gián đoạn và bất tiện.

## Mục tiêu
**Tự động** lưu trữ, đúc kết và duy trì liền mạch ngữ cảnh dự án cùng tri thức qua toàn bộ các thiết bị mà không bị ngắt quãng.

## Tiến độ
Chia làm 2 phần: **Core Engine & MCP Server** (trên 1 máy) làm trước, **Sync Layer** (đồng bộ đa thiết bị) làm sau.
- **Core Engine, MCP Server & Web Dashboard đã hoàn thiện và kiểm thử đầy đủ** (hỗ trợ Hook injection, Model Context Protocol stdio server với 4 tools, và Web Dashboard dòng thời gian).
- Sync Layer: Dự kiến ở Phase 2.

## Cài đặt nhanh (Khuyến nghị)

Script `install.sh` sẽ tự động:
1. Build 5 binary (`memremarkd`, `memremark-hook-claude-sessionstart`, `memremark-hook-antigravity-preinvocation`, `memremark-mcp`, `memremark-ui`) và cài đặt vào `~/.local/bin/`
2. Cài đặt và bật `systemd user service` để daemon tự chạy nền
3. **Smart patch** cấu hình hook và MCP server:
   - **Antigravity CLI**: Hook `PreInvocation` vào `~/.gemini/config/hooks.json` và MCP server `memremark` vào `~/.gemini/config/mcp_config.json`
   - **Claude Code**: Hook `SessionStart` vào `~/.claude/settings.json` và MCP server `memremark` vào `~/.claude/mcp.json`
   (Tự tạo bản sao lưu `.bak`, giữ nguyên các cấu hình/hook/MCP server khác).

```bash
# Cài đặt toàn bộ (cho cả Antigravity CLI và Claude Code):
./install.sh

# Hoặc chỉ cấu hình riêng cho Antigravity CLI:
./install.sh --cli=antigravity-cli

# Hoặc chỉ cấu hình riêng cho Claude Code:
./install.sh --cli=claude-code

# Để gỡ cài đặt hoàn toàn (gỡ service, xóa binary, gỡ hook & MCP server):
./install.sh --uninstall
```

Kiểm tra trạng thái daemon:
```bash
systemctl --user status memremarkd
```

Các lệnh quản lý daemon:
- Bật/chạy: `systemctl --user start memremarkd`
- Dừng: `systemctl --user stop memremarkd`
- Xem log trực tiếp: `journalctl --user -u memremarkd -f`

---

## Web Dashboard & Dòng thời gian (`memremark-ui`)

MemRemark tích hợp sẵn một Web Dashboard cục bộ độc lập được xây dựng bằng **Vue 3.5**, **TailwindCSS v4** và **Lucide Icons** nhúng trực tiếp bên trong file nhị phân Go (không cần cài đặt Node.js hay bất kỳ dependency nào khi chạy).

```bash
# Mở dashboard (tự động bật trình duyệt tại http://127.0.0.1:8765):
memremark-ui

# Tùy chỉnh cổng port hoặc mở mạng LAN (0.0.0.0):
memremark-ui --host 0.0.0.0 --port 9000

# Chạy ở chế độ không tự mở trình duyệt (thích hợp cho server SSH từ xa):
memremark-ui --no-open
```

### Tính năng nổi bật:
- **Timeline trực quan**: Dòng thời gian tri thức hiển thị dạng dọc, tự động nhóm theo ngày.
- **Hall Badges**: Phân loại màu sắc nhận diện rõ ràng (`📌 Fact`, `💡 Discovery`, `⚙️ Preference`, `🎯 Advice`, `⚡ Event`).
- **Tìm kiếm tức thì**: Live search từ khóa có debounce kèm phím tắt nhanh `/`.
- **Bộ chọn Workspace**: Lọc xem tri thức của từng dự án cụ thể hoặc tất cả các dự án.
- **Copy 1-click**: Sao chép nội dung tri thức vào clipboard nhanh chóng.
- **Dark / Light Theme**: Giao diện tối/sáng hiện đại, tự động lưu trạng thái.

---

## MCP Server & Tools (`memremark-mcp`)

`memremark-mcp` cung cấp giao thức **Model Context Protocol (MCP)** qua stdio kết nối trực tiếp với SQLite database, cho phép AI Agent chủ động tra cứu, ghi nhớ và quản lý ký ức theo ngữ cảnh dự án.

### Danh sách công cụ (Tools)

1. **`search_memory`**: Tìm kiếm ký ức theo từ khóa, danh mục hall, loại drawer hoặc workspace.
   - `query` *(string)*: Từ khóa tìm kiếm trong nội dung.
   - `hall` *(string, optional)*: Lọc theo danh mục (`fact`, `discovery`, `preference`, `advice`, `event`).
   - `type` *(string, optional)*: Lọc theo loại drawer (`summary`, `verbatim`, hoặc `all`).
   - `wing_path` *(string, optional)*: Đường dẫn thư mục workspace (mặc định: thư mục hiện tại).
   - `limit` *(integer, optional)*: Số lượng kết quả tối đa (mặc định: 10, tối đa: 50).

2. **`remember`**: Ghi nhớ tường minh một bài học, quy ước, quyết định hoặc phát hiện mới vào database.
   - `content` *(string, required)*: Nội dung cần ghi nhớ.
   - `hall` *(string, required)*: Phân loại ký ức (`fact`, `discovery`, `preference`, `advice`).
   - `wing_path` *(string, optional)*: Đường dẫn thư mục workspace (mặc định: thư mục hiện tại).

3. **`get_timeline`**: Xem dòng thời gian chi tiết các sự kiện/tóm tắt diễn ra theo thứ tự thời gian.
   - `session_id` *(string, optional)*: Lọc theo ID phiên làm việc cụ thể.
   - `wing_path` *(string, optional)*: Đường dẫn thư mục workspace (mặc định: thư mục hiện tại).
   - `since` *(integer, optional)*: Mốc Unix timestamp để lấy các sự kiện sau thời điểm đó.
   - `limit` *(integer, optional)*: Số lượng sự kiện tối đa (mặc định: 20, tối đa: 100).

4. **`forget_memory`**: Xóa một drawer ký ức lỗi thời hoặc sai lệch theo ID.
   - `id` *(integer, required)*: ID của drawer cần xóa.

---

## Đường dẫn mặc định

| Mục | Đường dẫn | Mô tả |
| :--- | :--- | :--- |
| **Database** | `~/.memremark/memremark.db` | Lưu trữ SQLite: danh sách wing, quan sát verbatim, tóm tắt và watermark |
| **Config File** | `~/.memremark/config.json` | Cấu hình model tóm tắt (`claude_model`, `antigravity_model`, `antigravity_effort`) |
| **Binaries** | `~/.local/bin/memremark*` | Danh sách binary sau khi cài đặt (`memremarkd`, hooks, MCP server, `memremark-ui`) |
| **Systemd** | `~/.config/systemd/user/memremarkd.service` | Quản lý tiến trình daemon tự khởi động |
| **Hook Antigravity** | `~/.gemini/config/hooks.json` | Cấu hình hook `PreInvocation` |
| **MCP Antigravity** | `~/.gemini/config/mcp_config.json` | Cấu hình MCP server cho Antigravity CLI |
| **Hook Claude Code** | `~/.claude/settings.json` | Cấu hình hook `SessionStart` |
| **MCP Claude Code** | `~/.claude/mcp.json` | Cấu hình MCP server cho Claude Code |

---

## Tùy biến cấu hình (`config.json`)

Mặc định, `memremarkd` tự động sử dụng các model nhẹ & rẻ nhất để tối ưu chi phí và RAM (`haiku` cho Claude Code, `gemini-3.7-flash-low` với `--effort low` cho Antigravity CLI).

Bạn có thể tùy chỉnh model và dashboard qua file `~/.memremark/config.json`:

```json
{
  "summarizer": {
    "claude_model": "haiku",
    "antigravity_model": "gemini-3.7-flash-low",
    "antigravity_effort": "low"
  },
  "ui": {
    "host": "127.0.0.1",
    "port": 8765,
    "auto_open": true
  }
}
```

* **Thứ tự ưu tiên cấu hình:** Biến môi trường (`MEMREMARK_*`) > File `config.json` > Mặc định hệ thống.
* **Biến môi trường hỗ trợ:**
  - `MEMREMARK_CLAUDE_MODEL`: Đổi model cho Claude (vd: `haiku`, `claude-3-5-sonnet`, `default`).
  - `MEMREMARK_ANTIGRAVITY_MODEL`: Đổi model cho Antigravity (vd: `gemini-3.7-flash-low`, `flash_lite`, `default`).
  - `MEMREMARK_ANTIGRAVITY_EFFORT`: Đổi mức reasoning effort (vd: `low`, `medium`, `high`, `default`).
  - `MEMREMARK_UI_HOST`: Tùy chỉnh host cho UI (vd: `0.0.0.0`, `127.0.0.1`).
  - `MEMREMARK_UI_PORT`: Tùy chỉnh cổng port cho UI (vd: `8765`, `9000`).
* **Lưu ý:** Đặt giá trị `"default"` hoặc `""` cho model nếu muốn daemon không truyền cờ `--model`, để CLI tự dùng model mặc định của bạn.

---

## Cấu hình Hooks & MCP thủ công (Manual)

Nếu không sử dụng `install.sh`, bạn có thể build và cấu hình thủ công:

### 1. Build binary
```bash
go build -o bin/memremarkd ./cmd/memremarkd
go build -o bin/memremark-hook-claude-sessionstart ./cmd/memremark-hook-claude-sessionstart
go build -o bin/memremark-hook-antigravity-preinvocation ./cmd/memremark-hook-antigravity-preinvocation
go build -o bin/memremark-mcp ./cmd/memremark-mcp
go build -o bin/memremark-ui ./cmd/memremark-ui
```

### 2. Cài hook cho Claude Code
Thêm vào `~/.claude/settings.json`:

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

### 3. Cài MCP server cho Claude Code
Thêm vào `~/.claude/mcp.json`:

```json
{
  "mcpServers": {
    "memremark": {
      "command": "/duong/dan/toi/memremark-mcp"
    }
  }
}
```

### 4. Cài hook cho Antigravity CLI
Thêm vào `~/.gemini/config/hooks.json`:

```json
{
  "memremark": {
    "PreInvocation": [
      {
        "type": "command",
        "command": "/duong/dan/toi/memremark-hook-antigravity-preinvocation",
        "timeout": 5
      }
    ]
  }
}
```

### 5. Cài MCP server cho Antigravity CLI
Thêm vào `~/.gemini/config/mcp_config.json`:

```json
{
  "mcpServers": {
    "memremark": {
      "command": "/duong/dan/toi/memremark-mcp"
    }
  }
}
```

---

## Giới hạn hiện tại

- Việc trích xuất nội dung từ Antigravity CLI (`internal/adapter/antigravity`) dùng phương pháp heuristic quét chuỗi trong dữ liệu protobuf thô (không có schema chính thức) — đủ dùng nhưng không đảm bảo trích xuất được cấu trúc tool/argument riêng biệt, chỉ có nội dung text thô.
- Chưa có Sync Layer — dữ liệu chỉ nằm trên từng máy riêng lẻ.

