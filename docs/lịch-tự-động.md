# Công việc chạy tự động theo lịch

Tài liệu này ghi lại hai phần:

1. Những gì đã tìm hiểu từ các loại *heartbeat* của OpenClaw và cơ chế `/loop`
   cùng scheduled tasks của Claude Code.
2. Thiết kế agent-tui rút ra từ đó.

## 1. Tham khảo

### OpenClaw

OpenClaw có hai cơ chế khác nhau về bản chất.

**Heartbeat: một lượt "đi tuần" định kỳ** ([docs/gateway/heartbeat.md][oc-hb])

- **Nhịp:** cứ `every` (mặc định 30m), agent đọc một checklist và xử lý mọi mục
  **trong cùng một turn**.
  - Checklist ban đầu là file `HEARTBEAT.md` trong workspace, nay là "monitor
    scratch".
  - Agent sửa được checklist ngay trong turn.
- **Hợp đồng im lặng:** không có gì cần báo thì agent trả lời `NO_REPLY` hoặc
  `HEARTBEAT_OK`.
  - Ack ở đầu hoặc cuối câu trả lời, phần còn lại ≤ 300 ký tự, thì bị nuốt.
  - Không ai bị làm phiền.
- **Checklist rỗng:** chỉ có heading, dòng trống hoặc comment thì bỏ qua cả lượt
  (`reason=empty-heartbeat-file`), không tốn API.
- **`activeHours`:** chỉ chạy trong khung giờ đã đặt (start, end, timezone).
- **Hoãn khi bận:**
  - Đang có turn hoặc automation chạy cho agent hay session đó thì heartbeat chờ.
  - Event-wake có giới hạn tần suất: tối thiểu 30 giây, chống dồn dập khi có 5
    lần trong 60 giây.
- **Chi phí:**
  - `isolatedSession` (session mới mỗi lượt) giảm từ khoảng 100K xuống còn
    2–5K token.
  - Thêm `lightContext` và một model rẻ hơn.

**Automations (cron): việc cụ thể theo lịch riêng** ([cron-jobs][oc-cron])

- **Kiểu lịch:**
  - `at`: một lần, ISO hoặc dạng tương đối như `20m`.
  - `every`: khoảng cố định.
  - `cron`: 5 hoặc 6 trường, có `--tz`.
  - Thêm các trigger sự kiện `on-exit` và `stream`.
  - Lịch đúng giờ chẵn tự giãn ngẫu nhiên tới 5 phút; `--exact` để tắt.
- **Payload:**
  - `system-event`: đưa một dòng vào session chính, không gọi model.
  - `message`: một agent turn, có model, thinking, tools riêng.
  - `command` hoặc `script`: chạy lệnh, không gọi model.
- **Session:**
  - `main`
  - `isolated`: `cron:<jobId>`, transcript mới mỗi lần.
  - `current`
  - `session:<id>`: một session có tên, nối tiếp lịch sử.
- **Pacing:** job đặt `pacing.min/max`, agent gọi `next_check(in)` để đề xuất lần
  sau. Đề xuất bị kẹp vào khoảng min/max; lần chạy lỗi thì bỏ đề xuất đó.
- **Condition watcher:** script trả `{fire, message, state}`.
  - Mỗi lần đánh giá có 30 giây và tối đa 5 tool call.
  - `fire:false` thì không gọi model.
- **Lỗi:**
  - Lỗi tạm thời (rate limit, mạng) thì lùi dần 30s → 60s → 5m → 15m → 60m;
    thành công thì reset.
  - Lỗi vĩnh viễn thì tắt job.
  - Cảnh báo sau 2 lần lỗi liên tiếp, cooldown 1 giờ. Cùng một nguyên nhân thì
    tính là một sự cố.
- **Lỡ lịch:** sau khi máy ngủ, mỗi mốc đã qua chạy một lần; tắt bằng
  `skipMissedJobs`.

### Claude Code

- **`/loop`** ([scheduled-tasks][cc-loop]): gắn với session đang mở.
  - Dạng lịch cố định: `/loop 5m <prompt>`, chuyển thành cron.
  - Dạng tự chọn nhịp: `/loop <prompt>`. Sau mỗi lượt, Claude gọi
    `ScheduleWakeup` với delay từ 1 phút đến 1 giờ kèm lý do, hoặc `stop:true`
    để kết thúc.
  - Nếu một lượt không hẹn lại thì có một lần đánh thức dự phòng khoảng 20 phút
    sau.
  - `/loop` không có prompt thì chạy prompt bảo trì, hoặc `.claude/loop.md`.
- **Khi nào chạy:** chỉ khi session rảnh, giữa các turn.
  - Bận thì chờ; không chạy bù từng lần lỡ.
  - Jitter cố định theo ID, tới nửa chu kỳ.
  - Job lặp tự hết hạn sau 7 ngày, chặn loop bị quên.
  - Tối đa 50 job mỗi session.
- **Tool:** `CronCreate`, `CronList`, `CronDelete`. Agent tự tạo được nhắc việc
  ("in 45 minutes, check…").
- **Desktop scheduled tasks** ([desktop][cc-desk]):
  - Mỗi lần chạy là một **session mới**, có thông báo.
  - Quyền và model đặt theo từng task; có thể dùng worktree riêng.
  - Lỡ lịch thì **chạy bù đúng một lần** cho mốc gần nhất trong 7 ngày.
  - Lịch sử ghi cả các lần bỏ qua và lý do (máy ngủ, lần trước chưa xong, task
    khác đang chạy).
  - Task tự sửa được lịch của mình.

[oc-hb]: https://raw.githubusercontent.com/openclaw/openclaw/main/docs/gateway/heartbeat.md
[oc-cron]: https://docs.openclaw.ai/automation/cron-jobs
[cc-loop]: https://code.claude.com/docs/en/scheduled-tasks
[cc-desk]: https://code.claude.com/docs/en/desktop-scheduled-tasks

### Điều rút ra

- **Hai nhu cầu khác nhau:**
  - *Đi tuần định kỳ:* một checklist, gộp trong một turn, im lặng khi ổn.
  - *Việc cụ thể theo lịch:* prompt riêng, lịch riêng, session riêng.
- **Nhịp tự điều chỉnh rất đáng giá:** agent biết khi nào nên quay lại. Nhưng
  phải có biên trên và biên dưới, và có lối dự phòng khi agent không hẹn.
- **Im lặng là mặc định đúng.** Một lịch chạy mỗi 30 phút mà lần nào cũng báo
  "không có gì" sẽ bị tắt ngay.
- **Nơi chạy phải độc lập với UI.** Claude Code `/loop` chết theo session; OpenClaw
  và Desktop chạy trong một tiến trình nền.
- **Lỡ lịch thì chạy bù một lần, không phải N lần.** Lỗi thì lùi dần rồi tự tạm
  dừng, đừng đốt token.

## 2. Thiết kế cho agent-tui

### Nơi chạy: gateway

Gateway giờ là service độc lập (xem `gateway-admin-và-bộ-nhớ.md`):

- Chạy nền, không phụ thuộc TUI hay web.
- Có Runner dùng chung engine, kit, MCP và learner với terminal.

Bộ lập lịch (`internal/schedule`) sống trong gateway:

- Gateway tắt thì không có gì chạy; khi bật lại thì chạy bù.
- TUI và web chỉ là chỗ xem và sửa lịch.

### Job

Một job gồm:

- `kind`:
  - `task`: prompt riêng.
  - `heartbeat`: checklist `<project>/.agent-tui/HEARTBEAT.md`.
- `root`: project; WSL cũng chạy được, như mọi session.
- Lịch:
  - `at` (một lần), `every`, hoặc `cron` 5 trường (giờ địa phương, OR giữa
    dom và dow như vixie-cron).
  - Tuỳ chọn `pacing {min,max}`: nhịp do agent chọn.
  - `active_hours {start,end,days}`.
- `session`:
  - `thread`: mặc định. Một session cho mỗi job (tên "⏰ <job>"), nên job chạy mỗi
    giờ không để lại 24 session mỗi ngày trong danh sách. Mỗi lần chạy bắt đầu với
    ngữ cảnh mới (`Session.ContextFrom`, `Command.Fresh`): engine không nhận các lần
    chạy trước, CLI không `--resume`. Người dùng trả lời trong session thì nối tiếp
    lần chạy mới nhất. Session bị xoá hoặc đóng thì lần sau tạo session mới.
  - `new`: session riêng mỗi lần chạy.
  - `same`: nối tiếp một session, như `/loop` hay `session:<id>`.
  - Với `same`, session có thể do TUI đang giữ: prompt được route tới TUI y như
    khi gửi từ web, nên lượt chạy hiện ngay trong terminal.
- `engine`, `model`, `mode`: theo job, mặc định của project.
  - Nên dùng mode `auto` hoặc `plan`.
  - Một lần chạy cần duyệt quyền thì treo ở đó, web hiện yêu cầu duyệt (giống
    Desktop).
- `gate`: lệnh shell chạy trước, không gọi model (giống condition watcher).
  - Exit khác 0 thì bỏ lượt (`skipped: gate`).
  - Có stdout thì gắn vào prompt.
  - Ví dụ: `gh pr checks 123 | grep -q fail`.
- `until`: hạn chót. Loop tạo từ `/loop` hết hạn sau 7 ngày như Claude Code.
- `delete_after_run` cho job `at`.

### Một lượt chạy

1. **Tới hạn:** bộ lập lịch kiểm tra mỗi 15 giây.
   - Lịch `cron` rơi đúng :00 hoặc :30 thì giãn cố định theo ID, tới 5 phút.
   - Ngoài `active_hours` thì đẩy tới đầu khung giờ kế tiếp.
2. **Hoãn khi bận:**
   - Job `same` mà session đang chạy turn thì chờ tick sau.
   - Tối đa 2 lượt chạy theo lịch cùng lúc; lượt dư thì chờ, không bỏ.
3. **Heartbeat có checklist rỗng** (chỉ heading, dòng trống, comment) thì
   `skipped: empty checklist`, không gọi model.
4. **Gate**, nếu có.
5. **Chạy:**
   - `new`, hoặc `thread` lần đầu: `Runner.NewJobSession`.
   - `thread` các lần sau: `Hub.Route(prompt, fresh)` tới session của job.
   - `same`: `Hub.Route(prompt)`, tới TUI đang giữ session hoặc tới Runner.
   - Prompt có dòng đầu `[scheduled: <tên> · <lý do>]`.
   - Heartbeat dùng prompt mặc định cộng nội dung checklist.
6. **Kết thúc:** bộ lập lịch nghe `turn.done` của đúng session đó, rồi đọc câu
   trả lời cuối.
   - Câu trả lời là `HEARTBEAT_OK` hoặc `NO_REPLY` ở đầu hay cuối, phần còn lại
     ≤ 300 ký tự, thì `silent`: không thông báo.
   - Còn lại thì `ok` và có thông báo (event `schedule.run`; web hiện toast).
   - Lỗi thì `error`.
7. **Lần kế tiếp:**
   - Agent đã đề xuất (tool `schedule`, action `next`) thì kẹp vào
     `pacing.min/max`.
   - Job có pacing mà agent không đề xuất thì dùng `pacing.max`.
   - Không thì theo lịch.
   - Lỗi thì lùi dần 30s → 1m → 5m → 15m → 60m; **5 lỗi liên tiếp thì tự tạm
     dừng** và ghi lý do.
8. **Quá giờ:** mặc định 30 phút thì huỷ turn.

**Lỡ lịch** (gateway tắt, máy ngủ): khi gateway khởi động, job nào có mốc đã qua
trong 7 ngày thì chạy bù **một lần** (`catch-up`); cũ hơn thì bỏ qua.

**Lịch sử:** mỗi lượt là một dòng trong `<data>/schedule/runs/<id>.jsonl`, gồm:

- giờ bắt đầu và kết thúc;
- trạng thái `ok`, `silent`, `error` hoặc `skipped` kèm lý do;
- session;
- trích đoạn câu trả lời.

Giữ 200 lượt gần nhất. Job và trạng thái nằm ở `<data>/schedule/jobs.json`.

### Agent tự lập lịch

Tool `schedule` cho cả engine built-in và engine claude (qua `kit-mcp`). Nó gọi
API của gateway với các action:

- `create`: "nhắc tôi lúc 3 giờ", "mỗi sáng 9 giờ review PR".
- `list`, `delete`.
- `next {in, reason}` hoặc `next {stop:true}`: nhịp tự chọn và dừng loop, như
  `ScheduleWakeup`.

### Giao diện

- **Web → Schedules:**
  - Danh sách job: lịch dạng chữ, lần chạy kế tiếp, trạng thái lần cuối.
  - Form tạo và sửa job.
  - Run now, Pause/Resume, Delete.
  - Lịch sử với link tới session.
  - Sửa `HEARTBEAT.md` ngay tại chỗ.
- **TUI:**
  - `/loop [khoảng] <prompt>`: lặp lại prompt trong *session này*.
    - Không có khoảng thì để agent tự chọn nhịp (1 phút đến 1 giờ).
    - Hết hạn sau 7 ngày.
  - `/loop stop`.
  - `/schedule`: liệt kê lịch của project.
