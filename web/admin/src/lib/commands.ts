// Commands the admin carries out itself rather than hand to the agent. Any
// other /word goes to the agent as typed — a skill, or one of its own
// commands — and a doubled slash sends a system command's name through as
// text ("//btw" reaches the agent as "/btw"), the terminal's rule.

export interface SystemCommand {
  name: string;
  /** args is the usage hint, shown after the name. */
  args?: string;
  /** summary is what it does, in a line. */
  summary: string;
  /** busyOnly: it means something only while a turn runs. */
  busyOnly?: boolean;
}

export const SYSTEM_COMMANDS: SystemCommand[] = [
  {
    name: "btw",
    args: "<question>",
    summary: "Ask on the side, with this conversation's context: the agent keeps working and the main thread is left alone",
  },
  {
    name: "fork",
    summary: "Branch this conversation into a new one, keeping the agent's context",
  },
  { name: "stop", summary: "Stop the running turn", busyOnly: true },
];

export interface ParsedCommand {
  cmd: SystemCommand;
  arg: string;
  /** end is where the command's name ends in the text: what to highlight. */
  end: number;
}

/** parseCommand reads a system command at the very start of the text. */
export function parseCommand(text: string): ParsedCommand | null {
  const m = /^\/([a-z][\w-]*)(?=\s|$)/i.exec(text);
  if (!m) return null;
  const cmd = SYSTEM_COMMANDS.find((c) => c.name === m[1].toLowerCase());
  if (!cmd) return null;
  return { cmd, arg: text.slice(m[0].length).trim(), end: m[0].length };
}

/** typingCommand is the name being typed while the text is only "/nam" —
 * when the menu of commands helps — or null. */
export function typingCommand(text: string): string | null {
  const m = /^\/([a-z-]*)$/i.exec(text);
  return m ? m[1].toLowerCase() : null;
}

/** agentCommand is a /word that is not ours: it goes to the agent. */
export function agentCommand(text: string): string | null {
  if (parseCommand(text)) return null;
  const m = /^\/([a-z][\w:.-]*)(?=\s|$)/i.exec(text);
  return m ? m[1] : null;
}

/** unescape turns "//btw" into "/btw" for the agent. */
export const unescapeSlash = (text: string) => (/^\/\/[a-z]/i.test(text) ? text.slice(1) : text);
