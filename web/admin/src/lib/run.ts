// Which pieces of an answer are commands to run, and where to run them.
//
// An agent's reply names commands in two ways: a fenced block in a shell's
// language, and a code span in a sentence ("run `npm test`"). A span is
// taken for a command only when it starts with a program one runs, so a
// variable name or a path stays plain code.

export type RunShell = { id: string; label: string };

const SHELL_LANGS = new Set(["bash", "sh", "shell", "zsh", "fish", "console", "terminal", "shellsession", "powershell", "ps1", "pwsh", "ps", "bat", "cmd", "batch"]);
const POWERSHELL_LANGS = new Set(["powershell", "ps1", "pwsh", "ps"]);

// Programs a suggested command plausibly starts with.
const PROGRAMS = new Set(
  (
    "python python3 py pip pip3 pipx uv poetry node npm npx pnpm yarn bun deno go cargo rustup make cmake git gh glab docker docker-compose podman kubectl helm k9s " +
    "terraform tofu aws gcloud az sam cdk serverless curl wget http ssh scp rsync ls cd cat less tail head grep rg find fd mkdir cp mv chmod chown touch tar unzip zip sudo apt apt-get " +
    "brew snap bash sh zsh fish pwsh powershell wsl code claude codex opencode agent-tui tui psql mysql redis-cli mongosh sqlite3 tsc eslint prettier pytest jest vitest playwright " +
    "dotnet java mvn gradle ./gradlew php composer ruby bundle rails rake export source echo env printenv systemctl journalctl kill pkill ps top htop df du ping nslookup dig " +
    "openssl jq yq sed awk xargs watch time nvm pyenv conda winget choco scoop ipconfig netstat taskkill tasklist start explorer notepad set setx cls dir type where"
  ).split(" "),
);

/** shellLang says whether a fenced block's language is a shell's. */
export const shellLang = (lang: string) => SHELL_LANGS.has(lang.toLowerCase());

/** asCommand is a block's text as the command to run: a "$ " prompt a
 * console transcript puts before each command is dropped, and the output
 * lines between such commands with it. */
export function asCommand(text: string): string {
  const lines = text.replace(/\n$/, "").split("\n");
  const prompted = lines.filter((l) => /^\s*[$>]\s/.test(l));
  if (prompted.length > 0 && prompted.length < lines.length) return prompted.map((l) => l.replace(/^\s*[$>]\s/, "")).join("\n");
  return lines.map((l) => l.replace(/^\s*\$\s/, "")).join("\n");
}

// Programs that are a command on their own, with nothing after them.
const BARE = new Set(["make", "ls", "pwd", "git", "npm", "pytest", "tui", "claude", "codex", "top", "htop", "k9s", "dir", "cls", "ipconfig"]);

/** looksRunnable says whether a line reads as a command: it starts with a
 * program, a script to execute (./run.sh, ./gradlew) or a PowerShell cmdlet. */
export function looksRunnable(line: string): boolean {
  const t = line.trim();
  if (!t || t.length > 2000) return false;
  const first = t.split(/\s+/)[0];
  if (PROGRAMS.has(first.toLowerCase())) return t.includes(" ") || BARE.has(first.toLowerCase());
  // A script by its path: one with a script's extension, or none (./gradlew); ./src/a.ts is a file.
  if (/^\.{1,2}\/[\w./-]+$/.test(first)) return /\.(sh|py|ps1|exe|bat|cmd)$/i.test(first) || !/\.\w+$/.test(first);
  return /^[A-Z][a-z]+-[A-Z][A-Za-z]+$/.test(first); // Get-ChildItem
}

/** onWindows says the command, or the sentence it came in, is meant for the
 * Windows side: a Windows variable, drive, program or the word itself. */
export function onWindows(command: string, context = ""): boolean {
  return (
    /\$env:|%\w+%|\$USERPROFILE\b|\$LOCALAPPDATA\b|\$APPDATA\b|\b[A-Z]:[\\/]|\.exe\b|\.ps1\b|\b(powershell|pwsh|winget|choco|scoop|setx|ipconfig|taskkill|tasklist)\b/i.test(command) ||
    /\bwindows\b/i.test(context)
  );
}

const powershellish = (c: string) => /\$env:|^[A-Z][a-z]+-[A-Z]|\|\s*[A-Z][a-z]+-[A-Z]|\bWrite-Host\b/m.test(c);
const bashish = (c: string) => /\$\w+|\$\(|&&|\|\||^export\s|^source\s|\.sh\b|<<|2>&1|~\//m.test(c);

/**
 * guessShell picks where a command runs, from the shells the project offers
 * (as the gateway lists them: the distribution's first for a project in WSL,
 * then the Windows ones). A PowerShell block goes to PowerShell; in a WSL
 * project, what is meant for Windows goes to Windows — Git Bash when it reads
 * like sh (it expands $USERPROFILE), PowerShell otherwise.
 */
export function guessShell(command: string, shells: RunShell[], lang = "", context = ""): string {
  if (!shells.length) return "";
  const ids = shells.map((s) => s.id);
  const has = (id: string) => ids.includes(id);
  const pick = (...want: string[]) => want.find(has);
  const ps = POWERSHELL_LANGS.has(lang.toLowerCase()) || powershellish(command);
  if (has("wsl")) {
    if (!ps && !onWindows(command, context)) return "wsl";
    if (ps) return pick("host-pwsh", "host-powershell") ?? "wsl";
    return (bashish(command) ? pick("host-bash", "host-powershell") : pick("host-powershell", "host-bash")) ?? "wsl";
  }
  if (ps) return pick("pwsh", "powershell") ?? ids[0];
  if (bashish(command)) return pick("bash", "sh") ?? ids[0];
  return ids[0];
}

/** clauseAround is the clause of a sentence a command sits in — up to the
 * nearest comma, full stop or line before and after it — so "build with
 * `make`, and on Windows run `setup.exe`" says Windows of the second only. */
export function clauseAround(text: string, command: string): string {
  const at = text.indexOf(command);
  if (at < 0) return "";
  const before = text.slice(0, at);
  const after = text.slice(at + command.length);
  const start = Math.max(...[",", ";", ".", "!", "?", "\n", "，", "。"].map((p) => before.lastIndexOf(p))) + 1;
  const ends = [",", ";", ".", "!", "?", "\n", "，", "。"].map((p) => after.indexOf(p)).filter((i) => i >= 0);
  return before.slice(start) + command + after.slice(0, ends.length ? Math.min(...ends) : after.length);
}
