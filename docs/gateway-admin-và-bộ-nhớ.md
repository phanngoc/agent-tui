# Gateway, web admin và bộ nhớ tự học

Tài liệu này mô tả kiến trúc của trang admin web (Next.js + shadcn + Tailwind +
Zustand), gateway nối TUI với web, và cơ chế tự học theo TencentDB Agent Memory.

## 1. Bức tranh tổng thể

```
 ┌──────────── tui (peer) ────────────┐        ┌──────── agent-tui serve (gateway) ─────────┐
 │ ui.Model ── startTurn ──► engine    │ events │ Hub: fan-out SSE, Live state, routing       │
 │   │  kit.Hook (memory/skill/MCP)    ├───────►│ Runner: chạy turn cho session không ai giữ  │◄── SSE ── web admin
 │   │  learner.Notify                 │◄───────┤   (cùng engine.Registry, kit, learner)      │── REST ─► (Next.js)
 │ commands: prompt/cancel/approve     │commands│ REST API: sessions, memory, skills, MCP,…   │
 └─────────────────────────────────────┘        └─────────────────────────────────────────────┘
                     │                                         │
                     └──────── cùng file trên đĩa ─────────────┘
          sessions/*.json · memory/ · skills/ · mcp.json · prefs.json · .agent-tui/
```

- **Một kiến trúc engine.** TUI và gateway dựng turn từ cùng các mảnh:
  `engine.Registry` (api/claude/codex/opencode), `kit.Hook` (gắn instructions,
  skills, memory, MCP vào turn), `learn.Learner` (tự học sau turn). Không có
  “engine của web” riêng.
- **Một từ vựng sự kiện** (`internal/gateway/wire.go`): `turn.started`,
  `text.delta`, `thinking.delta`, `tool.start|output|done`,
  `approval.request|resolved`, `choice.request|resolved`, `message`, `usage`,
  `turn.done`, `session.updated`, `learn`, `config.changed`. TUI publish đúng
  các sự kiện này từ `applyAgentEvent`; runner của gateway cũng vậy.
- **Một người ghi cho mỗi session.** TUI báo cho hub danh sách session nó đang
  giữ (`Hold`). Lệnh từ web cho các session đó được chuyển về TUI và xử lý như
  khi gõ trực tiếp (prompt → `startTurn`, approve → trả lời đúng approval đang
  chờ theo id). Session không TUI nào giữ thì gateway tự chạy; khi turn đó xong,
  hub gửi lệnh `open` cho mọi TUI đang mở cùng project → session hiện trong
  sidebar của TUI và prompt kế tiếp từ web sẽ chạy trong TUI.
- **Kết quả tool** được ghi vào message bằng cùng một hàm `Session.MarkTool`
  ở cả TUI lẫn gateway; web vá transcript ngay khi nhận `tool.done`.
- **Realtime.** Web nghe `/api/events` (SSE, có replay theo `Last-Event-ID`);
  `/api/live` cho trạng thái turn đang chạy khi trang mở giữa chừng.
- **Tự khởi động.** `tui` không thấy gateway (`<data>/gateway.json` hoặc
  `127.0.0.1:7788`) thì tự chạy `agent-tui serve` nền (tắt được trong Settings).
- **An toàn.** Chỉ nghe loopback; từ chối Host/Origin không phải loopback
  (chống DNS rebinding). Không có đăng nhập.

## 2. Global và project

| | global | project |
|---|---|---|
| skills | `~/.config/agent-tui/skills/` | `<repo>/.agent-tui/skills/` |
| MCP | `~/.config/agent-tui/mcp.json` | `<repo>/.agent-tui/mcp.json` |
| settings | `<data>/prefs.json` | `<repo>/.agent-tui/settings.json` |
| memory | `<data>/memory/` | `<data>/projects/<slug>/memory/` |

Thứ tự ưu tiên khi tạo session: flag › project › global › `config.json`.
Skill/MCP project cùng tên thay thế bản global; có thể tắt riêng từng cái cho
một project (`disabled_skills`, `disabled_mcp`). Memory project nằm ở thư mục
data chứ không trong repo.

Định dạng tương thích Claude Code nên trang admin import được skill, MCP server
và memory note từ `~/.claude` của máy host **và của từng distro WSL**
(`internal/claudecode`, đọc qua `\\wsl.localhost\<distro>\…`).

## 3. Một turn được cho thêm những gì (`internal/kit`)

1. Instructions global, instructions project, `AGENTS.md` của repo.
2. `<available_skills>`: tên + mô tả; nội dung nạp bằng tool `skill`.
3. Persona (global) + doctrine (project), các quy tắc “luôn áp dụng”
   (priority -1 hoặc ghim), bản đồ scene.
4. `<relevant-memories>`: top 5 record BM25 cho prompt này.
5. Tool: `memory_search`, `memory_read`, `memory_save`, và mọi tool MCP dạng
   `mcp__<server>__<tool>`.

Engine `api` gọi trực tiếp các tool này. Engine `claude` nhận cùng danh sách MCP
qua `--mcp-config`, cộng thêm server `agent-tui` (chính binary này chạy
`kit-mcp`) phục vụ tool skill/memory — nên hai engine có cùng năng lực.

Mỗi turn ghi một **trace** (`<data>/trace/<session>.jsonl`): memory nào được
recall và điểm, skill nào, MCP nào (lỗi kết nối), tool nào, toàn văn system text.
Trang Sessions hiển thị trace theo từng turn; Settings → Context preview cho xem
trước mà không chạy gì.

## 4. Tự học (`internal/learn`, `internal/memory`)

Port từ TencentDB Agent Memory (MemoryCore/src), chạy trong tiến trình, một
goroutine, một lần gọi model mỗi lúc, không bao giờ làm turn phải chờ.

| tầng | khi nào | làm gì |
|---|---|---|
| L0 | luôn | chính là `sessions/*.json` |
| L1 record | sau N turn (khởi động 1→2→4→5), hoặc 10 phút rảnh, hoặc “Learn now” | trích xuất memory tự đủ nghĩa (persona/instruction/episodic/work_fact/work_task/work_method/work_artifact) từ ≤10 tin nhắn mới + 5 tin nền; lọc theo ngưỡng priority |
| dedup | ngay sau L1 | mỗi memory mới so với 5 record gần nhất (BM25); một lần gọi judge: store / skip / update / merge; lỗi → store hết |
| L2 scene | sau L1, tối đa 15 phút/lần mỗi store | gộp record vào ≤15 scene Markdown (update > merge > create), heat tăng dần |
| L3 persona | scene yêu cầu, chưa có persona, hoặc ≥20 memory mới | viết lại persona (≤2000 ký tự) / doctrine (≤1200) |
| skill | session đạt 10 tool call | review transcript, tạo/cải thiện skill (đánh dấu `learned`) |

Mọi ghi đều vào log append-only (`memory/log/*.jsonl`) với `targets` bị thay
thế — đó là nguồn của tab History. Mỗi record giữ `session` + `sources` (chỉ số
tin nhắn) để truy ngược, và `hits` đếm số lần được recall.

Model học: `ANTHROPIC_API_KEY`/profile nếu có, không thì `claude -p` (không
tool, không MCP, không settings). Mặc định `claude-haiku-4-5`.

## 5. Chạy

```sh
make build && ./bin/agent-tui serve      # gateway + admin tĩnh (nếu đã build)
make web                                 # build web/admin → web/admin/out
make web-dev                             # dev server :3000 trỏ tới gateway :7788
```

Skill `deploy` build cả admin và copy vào thư mục `admin` cạnh binary, rồi dừng
gateway cũ để `tui` lần sau khởi động bản mới.
