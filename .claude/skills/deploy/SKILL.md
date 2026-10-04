---
name: deploy
description: Cài (hoặc cập nhật) agent-tui global trên máy này để chỉ cần `cd` vào thư mục bất kỳ rồi gõ `tui` là mở. Build từ source của repo bằng `go install`, tạo lệnh tắt `tui` cho PowerShell/cmd/Warp/Git Bash, thêm Go vào PATH của user nếu thiếu, rồi kiểm tra lệnh gọi được. Trigger - "deploy", "cài global", "install global", "cập nhật bản tui", "update tui trên máy", "tui không chạy / command not found", "sau khi sửa code muốn dùng bản mới".
---

# deploy — cài agent-tui global

Mục tiêu: mở terminal mới, `cd` vào thư mục bất kỳ, gõ `tui` (hoặc `agent-tui`) là ra
agent-tui với thư mục đó làm project root (mặc định `-C "."`).

## Windows (máy chính)

Chạy script, không tự gõ lại từng bước:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .claude/skills/deploy/install.ps1
```

Tùy chọn: `-Alias <tên>` để đổi tên lệnh tắt (mặc định `tui`; truyền `-Alias ''` để bỏ).

Script làm, theo thứ tự, và chạy lại bao nhiêu lần cũng an toàn:

1. **Tìm `go`** — trên PATH, nếu không có thì ở `%LOCALAPPDATA%\Programs\Go\bin`,
   `C:\Program Files\Go\bin`, scoop. Máy này cài Go ở LocalAppData mà installer
   *không* thêm vào PATH — "go: command not found" không có nghĩa là chưa cài Go.
   Không tìm thấy thật thì dừng và gợi ý `winget install GoLang.Go`, đừng tự cài.
2. **`go install -trimpath -ldflags="-s -w" ./cmd/agent-tui`** → `GOBIN` hoặc
   `%USERPROFILE%\go\bin\agent-tui.exe`. Cùng flag với `make install`.
2b. **Bản desktop** (`desktop/`, module riêng): `go build -C desktop -ldflags "-s -w -H windowsgui"`
   → `agent-tui-desktop.exe` cạnh `agent-tui.exe`, và shortcut **agent-tui** trong Start menu.
   Nó chạy chính `agent-tui.exe` không sửa đổi trong ConPTY, vẽ bằng Gio (GPU, không cgo).
2c. **Web admin** (`web/admin`, Next.js): nếu có `npm` thì `npm run build` (static export) rồi copy
   `web/admin/out` → thư mục `admin` cạnh `agent-tui.exe`; `agent-tui serve` tự phục vụ từ đó.
   Gateway (`agent-tui serve`) đang chạy bản cũ bị dừng để `tui` lần sau khởi động bản mới.
3. **Lệnh tắt `tui`** trong cùng thư mục bin: `tui.cmd` (PowerShell/cmd/Warp) và
   `tui` không đuôi (Git Bash không tự resolve `.cmd`). Cả hai chuyển mọi tham số
   sang `agent-tui.exe`. Không dùng hardlink/copy exe: `go install` thay file nên
   link sẽ trỏ vào bản cũ.
4. **PATH của user** (không phải Machine — không cần admin): thêm thư mục Go và
   thư mục bin nếu chưa có. Không bao giờ ghi đè hay sắp xếp lại PATH sẵn có.
5. **Kiểm tra** với PATH dựng lại như một terminal mới và chạy `agent-tui.exe -h`.

## macOS / Linux

```sh
make install                      # → $(go env GOPATH)/bin/agent-tui
ln -sf "$(go env GOPATH)/bin/agent-tui" "$(go env GOPATH)/bin/tui"
```

Symlink theo tên nên vẫn đúng sau mỗi lần `go install`. Nếu `$(go env GOPATH)/bin`
chưa có trong PATH, thêm `export PATH="$PATH:$(go env GOPATH)/bin"` vào
`~/.zshrc` / `~/.bashrc` — hỏi người dùng trước khi sửa file rc.

## Sau khi chạy

Báo lại cho người dùng:
- commit đã cài (script in ra) và đường dẫn binary;
- PATH có đổi không — nếu có, nhắc **mở terminal mới**, cửa sổ đang mở không nhận PATH mới;
- dòng `verify` có OK không. Nếu script lỗi, đưa nguyên văn lỗi, đừng báo là xong.

Chỉ chạy `tui -h` để kiểm tra; đừng mở TUI thật trong tool (nó chiếm terminal và treo).

Cần credential Anthropic để engine `api` chạy được: `ANTHROPIC_API_KEY` hoặc
`ant auth login` — việc của người dùng, skill này không đụng tới.
