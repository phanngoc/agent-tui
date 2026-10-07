import { toast } from "sonner";
import { useGateway } from "@/lib/store";
import { useChatDraft } from "@/lib/draft";
import { describePath } from "@/components/folder-picker";

// What the right-click menus of the editor's tabs and explorer do to a file.

/** fullPath is a file of the project as its own filesystem names it: the Linux path inside WSL. */
export function fullPath(root: string, rel: string): string {
  const d = describePath(root);
  if (d.where) return d.dir.replace(/\/+$/, "") + "/" + rel;
  const sep = root.includes("\\") ? "\\" : "/";
  return root.replace(/[\\/]+$/, "") + sep + rel.split("/").join(sep);
}

export function copyText(text: string, what: string) {
  return navigator.clipboard.writeText(text).then(
    () => toast.success(`${what} copied`, { description: text }),
    () => toast.error("Could not copy"),
  );
}

/** addFileToChat opens a new chat in the project with the file named in its composer. */
export function addFileToChat(root: string, rel: string, go: (href: string) => void) {
  useGateway.getState().setRoot(root);
  useChatDraft.getState().push("`" + rel + "`");
  go("/sessions");
}
