Đã tra cứu — và hoá ra cả ba hệ kia đều vừa bỏ file phẳng để sang SQLite trong năm
2026. Đó mới là điểm đáng chú ý chứ không phải chuyện JSON hay JSONL.

Năm hình dạng

Hình dạng
agent-tui: 1 JSON / session
Claude Code / Codex: JSONL append-only
opencode v1.2+: SQLite
OpenClaw 2026.8.1+: SQLite / agent
Hermes: SQLite + WAL
────────────────────────────────────────
Đường dẫn
agent-tui: sessions/<id>.json
Claude Code / Codex: <session-id>.jsonl
opencode v1.2+: opencode.db
OpenClaw 2026.8.1+: agents/<id>/agent/openclaw-agent.sqlite
Hermes: ~/.hermes/state.db
────────────────────────────────────────
Đơn vị ghi
agent-tui: rewrite cả file
Claude Code / Codex: append 1 dòng
opencode v1.2+: INSERT 1 part row
OpenClaw 2026.8.1+: INSERT 1 transcript event
Hermes: INSERT 1 message row
────────────────────────────────────────
Cấu trúc transcript
agent-tui: mảng phẳng
Claude Code / Codex: mảng phẳng
opencode v1.2+: message tách thành nhiều part
OpenClaw 2026.8.1+: cây (id/parentId)
Hermes: phẳng, ~20 cột/row
────────────────────────────────────────
Đọc 1 trang
agent-tui: Unmarshal cả file
Claude Code / Codex: quét cả file
opencode v1.2+: SELECT + join
OpenClaw 2026.8.1+: bounded tail read
Hermes: SELECT LIMIT
────────────────────────────────────────
Search
agent-tui: không
Claude Code / Codex: grep
opencode v1.2+: SQL
OpenClaw 2026.8.1+: SQL
Hermes: FTS5 ×2 (word + trigram cho CJK)
────────────────────────────────────────
Nén / xoá bớt
agent-tui: không có
Claude Code / Codex: phải compact thủ công
opencode v1.2+: —
OpenClaw 2026.8.1+: active events ↔ transcript_archives
Hermes: active=0 archive tại chỗ, prune >90 ngày
────────────────────────────────────────
Trước đây dùng gì
agent-tui: —
Claude Code / Codex: —
opencode v1.2+: per-file JSON dưới storage/session
OpenClaw 2026.8.1+: JSONL dưới sessions/, đổi 2026-09-01
Hermes: DB ngay từ đầu

Hai cái mới: ưu nhược

OpenClaw — transcript là cây append-only, mỗi entry có id và parentId.

Ưu: fork/branch là miễn phí. Rẽ nhánh hội thoại chỉ là thêm một node trỏ về cha,
không copy gì cả. Compaction không đẻ ra file checkpoint trùng lặp mà chỉ ghi
metadata trỏ tới transcript kế nhiệm. Gateway không bao giờ dựng lại cả transcript
— hiển thị trang đầu, khôi phục sau restart, đếm token đều là truy vấn có giới hạn.

Nhược: một DB cho mỗi agent, nên không query xuyên agent được. Cây khó debug hơn
danh sách — bạn không "đọc" được nó, phải duyệt. Và họ trả giá đúng nghĩa:
migration 2026-09-01 làm hỏng loạt công cụ bên thứ ba vẫn đang đọc .jsonl
(agentsview, codeburn, llm-systems-manager đều có issue dashboard trống).

Hermes — SQLite + WAL, một row một message, ~20 cột.

Ưu: WAL cho nhiều reader + một writer, nên CLI, gateway và agent process dùng chung
một cài đặt mà không giẫm chân nhau — đây là lý do thật sự chọn DB, không phải tốc
độ. FTS5 hai bản: một word-based, một trigram tokenizer cho CJK và tìm chuỗi con —
với tiếng Việt thì bản trigram là cái đáng giá. Có cột api_content giữ nguyên
bytes wire để prompt cache không vỡ. Có schema_version và migration đàng hoàng.

Nhược: 10 bảng, trigger đồng bộ FTS, khoá compaction xuyên process, retry có jitter
khi tranh ghi. Đó là hạ tầng, và bạn phải nuôi nó kể cả khi chỉ có 7 session.
Chính docs thừa nhận FTS thay cách làm cũ ở schema version 23 — tức là 23 lần
migration phải viết và phải test.

Cái này nói gì về agent-tui

Ba hệ kia chuyển sang DB vì bốn áp lực cụ thể: nhiều process dùng chung, đọc có
giới hạn (đừng dựng cả transcript chỉ để đếm token), compaction sửa tại chỗ, và
search. agent-tui hiện không có cái nào — một process, một người dùng, và
transcript vốn đã dựng lại toàn bộ mỗi frame (có cache).

Nhưng có hai thứ đang tới, và OpenClaw đã giải đúng nó:

1. Fork và /btw đang copy nguyên lịch sử. Manager.Fork sao chép cả []Message sang
session mới, rồi ghi ra file riêng. Nghĩa là /btw trên session 479 KB kia sẽ đẻ
thêm một file 479 KB nữa — nội dung y hệt. Trên đĩa bạn chưa có fork nào (tính năng
còn mới) nên đây là chi phí dự đoán, chưa phải đo được. Cây id/parentId của
OpenClaw chính xác là lời giải cho việc này.

2. Search xuyên session. Hiện muốn tìm "session nào nhắc resolvePfid" là phải đọc
cả 7 file rồi lọc. Nó ổn ở 7 file; ở 200 thì không.

Khuyến nghị của tôi không đổi: JSONL append-only cho messages + JSON nhỏ cho metadata. Nó xử lý được write amplification bậc hai, giữ được grep/jq, không cần schema migration. Nếu sau này cần fork rẻ và search thì mới tính SQLite — và lúc đó nhớ bài học OpenClaw: đổi format là phá vỡ mọi thứ đang đọc file cũ, nên phải kèm đường migrate.                                                                                    Vẫn chưa đụng gì vào code cả.                                                     
Sources:
- Hermes Agent — Session Storage · schema trên GitHub
- OpenClaw — Session state on disk · Database layout- OpenClaw 2026.9.x: storage moved from sessions/*.jsonl to SQLite                  - Where AI coding agents store your session history                               
Ba hệ kia chuyển sang DB vì bốn áp lực cụ thể: nhiều process dùng chung, đọc có giới hạn (đừng dựng cả transcript chỉ để đếm token), compaction sửa tại chỗ, và search. agent-tui hiện không có cái nào — một process, một người dùng, và transcript vốn đã dựng lại toàn bộ mỗi frame (có cache).

Nhưng có hai thứ đang tới, và OpenClaw đã giải đúng nó:

1. Fork và /btw đang copy nguyên lịch sử. Manager.Fork sao chép cả []Message sang session mới, rồi ghi ra file riêng. Nghĩa là /btw trên session 479 KB kia sẽ đẻ thêm một file 479 KB nữa — nội dung y hệt. Trên đĩa bạn chưa có fork nào (tính năng còn mới) nên đây là chi phí dự đoán, chưa phải đo được. Cây id/parentId của OpenClaw chính xác là lời giải cho việc này.

2. Search xuyên session. Hiện muốn tìm "session nào nhắc resolvePfid" là phải đọc cả 7 file rồi lọc. Nó ổn ở 7 file; ở 200 thì không.

Khuyến nghị của tôi không đổi: JSONL append-only cho messages + JSON nhỏ cho metadata. Nó xử lý được write amplification bậc hai, giữ được grep/jq, không cần schema migration. Nếu sau này cần fork rẻ và search thì mới tính SQLite — và lúc đó nhớ bài học OpenClaw: đổi format là phá vỡ mọi thứ đang đọc file cũ, nên phải kèm đường migrate.
---

Tiếp theo: [mô-hình-vận-hành.md](mô-hình-vận-hành.md) — vì sao mỗi hệ có hình dạng đó,
và điều kiện kích hoạt để agent-tui đổi trường phái.
