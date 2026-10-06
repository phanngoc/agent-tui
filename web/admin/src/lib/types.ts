// Shapes of the gateway's JSON, mirroring the Go structs.

export type Scope = "global" | "project";

export interface ToolCall {
  id: string;
  name: string;
  input: unknown;
  result?: string;
  is_error?: boolean;
  done: boolean;
  denied?: boolean;
  chosen?: string;
  elapsed?: number; // nanoseconds
  agent?: SubAgent;
}

/** SubAgent is an agent started by another (Claude Code's Agent tool). */
export interface SubAgent {
  id?: string;
  type?: string;
  description?: string;
  prompt?: string;
  state: "starting" | "running" | "done" | "failed" | "stopped" | "";
  activity?: string;
  last_tool?: string;
  tokens?: number;
  tool_uses?: number;
  duration?: number; // nanoseconds
  depth?: number;
  background?: boolean;
  started?: string;
  ended?: string;
  summary?: string;
  calls?: ToolCall[];
}

export interface Message {
  role: "user" | "assistant";
  text?: string;
  thinking?: string;
  tools?: ToolCall[];
  at: string;
  err?: string;
  shell?: { command: string; output?: string; exit: number; where?: string };
  files?: { path: string; media?: string }[];
}

export interface Session {
  id: string;
  title: string;
  root: string;
  model: string;
  engine?: string;
  mode?: string;
  target?: string;
  cwd?: string;
  created: string;
  updated: string;
  messages: Message[];
  input_tokens: number;
  output_tokens: number;
  job?: string; // the scheduled job this session is a run of
  cache_reads: number;
  engines?: Record<string, { external_id?: string; seen?: number }>;
}

export interface Summary {
  id: string;
  title: string;
  root: string;
  engine?: string;
  model?: string;
  mode?: string;
  target?: string;
  cwd?: string;
  messages: number;
  created: string;
  updated: string;
  busy: boolean;
  status?: string;
  owner?: string;
  job?: string; // the scheduled job this session is a run of
  input_tokens: number;
  output_tokens: number;
  closed?: boolean;
  side_of?: string;
}

export interface ApprovalData {
  id: string;
  call: ToolCall;
  reason: string;
}

export interface ChoiceData {
  id: string;
  call: ToolCall;
  question: string;
  options: { label: string; detail?: string }[];
}

export interface Live {
  session: string;
  owner: string;
  busy: boolean;
  status?: string;
  prompt?: string;
  engine?: string;
  started?: string;
  partial?: string;
  thinking?: string;
  running: Record<string, ToolCall>;
  output: Record<string, string>;
  approvals: Record<string, ApprovalData>;
  choices: Record<string, ChoiceData>;
  error?: string;
  agents?: Record<string, SubAgent>;
}

export interface GatewayEvent {
  seq: number;
  at: string;
  type: string;
  session?: string;
  root?: string;
  origin?: string;
  data?: any; // eslint-disable-line @typescript-eslint/no-explicit-any
}

export interface Peer {
  id: string;
  kind: string;
  root: string;
  pid: number;
  joined: string;
  seen: string;
  sessions: string[];
}

export interface Project {
  root: string;
  name: string;
  sessions: number;
  updated: string;
  exists: boolean;
  config: boolean;
  peers?: string[];
}

export interface TraceHit {
  id: string;
  scope: Scope;
  type: string;
  content: string;
  score: number;
}

export interface Trace {
  at: string;
  session: string;
  engine: string;
  prompt: string;
  recalled: TraceHit[];
  standing: number;
  skills: string[];
  mcp: string[];
  mcp_errors?: string[];
  tools: string[];
  persona: boolean;
  doctrine: boolean;
  system_chars: number;
  system: string;
  learning: boolean;
}

export interface MemoryRecord {
  id: string;
  content: string;
  type: string;
  priority: number;
  scene?: string;
  scope: Scope;
  session?: string;
  sources?: number[];
  metadata?: Record<string, unknown>;
  timestamps?: string[];
  created: string;
  updated: string;
  version: number;
  origin?: string;
  pinned?: boolean;
  hits?: number;
  last_hit?: string;
}

export interface MemoryStats {
  scope: Scope;
  records: number;
  by_type: Record<string, number>;
  scenes: number;
  persona: boolean;
  updated: string;
  dir: string;
}

export interface LogEntry {
  at: string;
  op: string;
  record: MemoryRecord;
  targets?: string[];
}

export interface Scene {
  file: string;
  summary: string;
  heat: number;
  created: string;
  updated: string;
  body: string;
}

export interface Activity {
  at: string;
  stage: string;
  session?: string;
  root?: string;
  detail: string;
  error?: string;
  records?: string[];
  scope?: string;
  dir?: string;
}

export interface SessionState {
  root: string;
  cursor: number;
  turns: number;
  threshold: number;
  scene?: string;
  skill_cursor: number;
  updated: string;
}

export interface LearnStatus {
  model: string;
  error?: string;
  busy?: string;
  queue: number;
  sessions: Record<string, SessionState>;
  stores: Record<string, { last_scenes: string; pending?: string[]; since_persona: number; want_persona?: boolean }>;
}

export interface Skill {
  name: string;
  description: string;
  body: string;
  scope: Scope;
  path: string;
  files?: string[];
  disabled?: boolean;
  shadowed?: boolean;
  learned?: boolean;
  updated: string;
}

export interface McpServer {
  name: string;
  type?: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  disabled?: boolean;
  scope: Scope;
  shadowed?: boolean;
  off?: boolean;
  signed_in?: boolean;
}

export interface McpTool {
  name: string;
  description?: string;
  inputSchema?: unknown;
}

export interface McpStatus {
  name: string;
  connected: boolean;
  error?: string;
  tools: McpTool[];
  server_info?: string;
  at: string;
  needs_auth?: boolean;
  signed_in?: boolean;
}

export interface Install {
  id: string;
  label: string;
  home: string;
  linux_home?: string;
  skills: { install: string; name: string; description: string; dir: string }[];
  servers: (Omit<McpServer, "scope"> & { install: string; project?: string })[];
  memory: { install: string; project: string; file: string; name: string; description: string; type: string; body: string }[];
}

export interface Prefs {
  start_dir?: string;
  start_path?: string;
  engine?: string;
  model?: string;
  mode?: string;
  theme?: string;
  last_root?: string;
  effort?: string;
  learn?: boolean | null;
  learn_model?: string;
  learn_skills?: boolean | null;
  gateway_autostart?: boolean | null;
  instructions?: string;
  keep_awake?: string;
  keep_display?: boolean;
}

export interface ProjectSettings {
  engine?: string;
  model?: string;
  mode?: string;
  effort?: string;
  learn?: boolean | null;
  disabled_skills?: string[];
  disabled_mcp?: string[];
  instructions?: string;
}

export interface EngineInfo {
  id: string;
  label: string;
  detail: string;
  available: boolean;
  can_ask: boolean;
}

export interface ScheduleJob {
  id: string;
  name: string;
  kind: "task" | "heartbeat";
  root: string;
  prompt?: string;
  at?: string;
  every?: string;
  cron?: string;
  pacing?: { min: string; max: string } | null;
  active_hours?: { start: string; end: string; days?: number[] } | null;
  until?: string;
  session?: "thread" | "new" | "same";
  session_id?: string;
  engine?: string;
  model?: string;
  mode?: string;
  gate?: string;
  timeout?: string;
  delete_after_run?: boolean;
  skip_missed?: boolean;
  enabled: boolean;
  created?: string;
  origin?: string;
  describe?: string;
  state: {
    next?: string;
    last_run?: string;
    last_status?: string;
    last_error?: string;
    last_text?: string;
    failures?: number;
    runs?: number;
    running?: string;
  };
}

export interface ScheduleRun {
  id: string;
  job: string;
  start: string;
  end?: string;
  status: string;
  reason?: string;
  session?: string;
  text?: string;
  error?: string;
  skipped?: string;
}
