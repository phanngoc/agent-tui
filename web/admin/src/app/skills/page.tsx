"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { PlusIcon, DownloadIcon, GraduationCapIcon, Trash2Icon, FileIcon, EyeOffIcon } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { useFetch } from "@/lib/hooks";
import { useGateway, useVersion } from "@/lib/store";
import type { Install, Scope, Skill } from "@/lib/types";
import { Ago, Empty, ErrorNote, Field, Mono, NativeSelect, PageHeader, ScopeBadge } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { PaneToggle, Workspace } from "@/components/workspace";

export default function SkillsPage() {
  return (
    <React.Suspense fallback={null}>
      <Skills />
    </React.Suspense>
  );
}

interface SkillList {
  skills: Skill[];
  global_dir: string;
  project_dir: string;
}

function Skills() {
  const root = useGateway((s) => s.root);
  const params = useSearchParams();
  const router = useRouter();
  const v = useVersion("skills");
  const { data, error } = useFetch<SkillList>("/api/skills" + qs({ root }), [v, root]);
  const [importing, setImporting] = React.useState(false);
  const want = params.get("name") ?? "";
  const wantScope = params.get("scope") ?? "";
  const [draft, setDraft] = React.useState<Skill | null>(null);

  const skills = data?.skills ?? [];
  const selected = draft ?? skills.find((s) => s.name === want && (!wantScope || s.scope === wantScope) && !s.shadowed) ?? skills.find((s) => s.name === want) ?? null;
  const select = (s: Skill) => {
    setDraft(null);
    router.replace(`/skills?name=${s.name}&scope=${s.scope}`);
  };

  const groups: { scope: Scope; title: string; dir: string }[] = [
    ...(root ? [{ scope: "project" as Scope, title: "Project skills", dir: data?.project_dir ?? "" }] : []),
    { scope: "global", title: "Global skills", dir: data?.global_dir ?? "" },
  ];

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="Skills"
        description="Named procedures the agent loads when a task calls for one. SKILL.md files, compatible with Claude Code. A project skill replaces a global one of the same name."
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => setImporting(true)}>
              <DownloadIcon /> Import from Claude Code
            </Button>
            <Button
              size="sm"
              onClick={() => setDraft({ name: "", description: "", body: "## When to use\n\n## Workflow\n1. \n", scope: root ? "project" : "global", path: "", updated: "" })}
            >
              <PlusIcon /> New skill
            </Button>
          </>
        }
      />
      <ErrorNote error={error} className="m-6" />
      <div className="min-h-0 flex-1">
        <Workspace selection={want || (draft ? "draft" : "")}
          id="skills"
          left={{
            node: (
              <div className="h-full overflow-auto p-3">
          {groups.map((g) => {
            const list = skills.filter((s) => s.scope === g.scope);
            return (
              <div key={g.scope} className="mb-5">
                <div className="mb-1 flex items-center gap-2 px-1">
                  <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{g.title}</span>
                  <span className="text-xs text-muted-foreground">{list.length}</span>
                </div>
                <div className="mb-2 truncate px-1 font-mono text-[10px] text-muted-foreground" title={g.dir}>
                  {g.dir}
                </div>
                {list.length === 0 && <div className="px-1 text-xs text-muted-foreground">none</div>}
                {list.map((s) => (
                  <button
                    key={s.scope + s.name}
                    onClick={() => select(s)}
                    className={cn(
                      "mb-1 w-full rounded-lg border px-2.5 py-2 text-left hover:bg-muted/50",
                      selected && selected.name === s.name && selected.scope === s.scope && "border-primary bg-muted",
                      (s.shadowed || s.disabled) && "opacity-60",
                    )}
                  >
                    <div className="flex items-center gap-1.5">
                      <span className="truncate text-sm font-medium">{s.name}</span>
                      {s.learned && <GraduationCapIcon className="size-3.5 text-emerald-600" />}
                      {s.disabled && <EyeOffIcon className="size-3.5" />}
                      {s.shadowed && (
                        <Badge variant="outline" className="ml-auto text-[10px] font-normal">
                          replaced
                        </Badge>
                      )}
                    </div>
                    <div className="line-clamp-2 text-xs text-muted-foreground">{s.description}</div>
                  </button>
                ))}
              </div>
            );
          })}
              </div>
            ),
            defaultSize: 320,
            minSize: 220,
            maxSize: 560,
            foldBelow: 820,
            label: "skill list",
          }}
        >
          <div className="min-h-0 flex-1 overflow-auto p-6">
            <PaneToggle side="left" className="-mt-3 -ml-3 mb-1" />
          {selected ? (
            <SkillEditor key={(selected.scope ?? "") + selected.name + (draft ? "draft" : "")} skill={selected} isNew={!!draft} root={root} onSaved={(s) => select(s)} onDeleted={() => router.replace("/skills")} />
          ) : (
            <Empty title="Pick a skill">
              The agent sees each enabled skill&apos;s name and description in its system prompt and loads the body with the <Mono>skill</Mono> tool. Skills marked
              <GraduationCapIcon className="mx-1 inline size-3.5 text-emerald-600" />
              were written by the learner from a session&apos;s work.
            </Empty>
          )}
          </div>
        </Workspace>
      </div>
      <ImportDialog open={importing} onOpenChange={setImporting} root={root} />
    </div>
  );
}

function SkillEditor({ skill, isNew, root, onSaved, onDeleted }: { skill: Skill; isNew: boolean; root: string; onSaved: (s: Skill) => void; onDeleted: () => void }) {
  const [s, setS] = React.useState(skill);
  const [err, setErr] = React.useState<string | null>(null);
  const dirty = JSON.stringify(s) !== JSON.stringify(skill);
  const save = async () => {
    setErr(null);
    try {
      const saved = await api.put<Skill>("/api/skills", { root, old_name: isNew ? "" : skill.name, old_scope: isNew ? "" : skill.scope, skill: s });
      toast.success("Skill saved");
      onSaved(saved);
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  return (
    <div className="mx-auto max-w-4xl space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-lg font-semibold">{isNew ? "New skill" : skill.name}</h2>
        {!isNew && <ScopeBadge scope={skill.scope} />}
        {skill.learned && (
          <Badge className="gap-1 bg-emerald-600 font-normal text-white">
            <GraduationCapIcon className="size-3" /> learned
          </Badge>
        )}
        {skill.shadowed && <Badge variant="outline">replaced by the project&apos;s {skill.name}</Badge>}
        {!isNew && <span className="text-xs text-muted-foreground">updated <Ago at={skill.updated} /></span>}
        {!isNew && root && (
          <label className="ml-auto flex items-center gap-2 text-sm">
            <Switch
              checked={!skill.disabled}
              onCheckedChange={async (on) => {
                try {
                  await api.post("/api/project/toggle", { root, kind: "skill", name: skill.name, enabled: on });
                  toast.success(on ? "Enabled for this project" : "Disabled for this project");
                } catch (e) {
                  toast.error((e as Error).message);
                }
              }}
            />
            enabled in this project
          </label>
        )}
      </div>
      {!isNew && (
        <div className="font-mono text-xs text-muted-foreground">
          {skill.path}
          {skill.files && skill.files.length > 0 && (
            <div className="mt-1 flex flex-wrap gap-2">
              {skill.files.map((f) => (
                <span key={f} className="inline-flex items-center gap-1">
                  <FileIcon className="size-3" />
                  {f}
                </span>
              ))}
            </div>
          )}
        </div>
      )}
      <div className="grid gap-3 md:grid-cols-[1fr_12rem]">
        <Field label="Name" hint="lowercase letters, digits, . _ -">
          <Input value={s.name} onChange={(e) => setS({ ...s, name: e.target.value })} className="font-mono" />
        </Field>
        <Field label="Scope" hint="moving it moves the folder">
          <NativeSelect
            value={s.scope}
            onChange={(v) => setS({ ...s, scope: v as Scope })}
            options={[...(root ? [{ value: "project", label: "project (.agent-tui/skills)" }] : []), { value: "global", label: "global" }]}
          />
        </Field>
      </div>
      <Field label="Description" hint="What it does and when to use it, with trigger phrases — this is all the agent sees until it loads the skill.">
        <Textarea rows={3} value={s.description} onChange={(e) => setS({ ...s, description: e.target.value })} />
      </Field>
      <Field label="Body (SKILL.md, Markdown)">
        <Textarea rows={22} value={s.body} onChange={(e) => setS({ ...s, body: e.target.value })} className="font-mono text-xs leading-relaxed" />
      </Field>
      <ErrorNote error={err} />
      <div className="flex gap-2">
        <Button onClick={save} disabled={!dirty && !isNew}>
          Save
        </Button>
        {!isNew && (
          <Button
            variant="destructive"
            onClick={async () => {
              if (!confirm(`Delete the ${skill.scope} skill ${skill.name} and its folder?`)) return;
              try {
                await api.del("/api/skills" + qs({ root, scope: skill.scope, name: skill.name }));
                toast.success("Deleted");
                onDeleted();
              } catch (e) {
                toast.error((e as Error).message);
              }
            }}
          >
            <Trash2Icon /> Delete
          </Button>
        )}
      </div>
    </div>
  );
}

function ImportDialog({ open, onOpenChange, root }: { open: boolean; onOpenChange: (o: boolean) => void; root: string }) {
  const { data, loading, error } = useFetch<Install[]>(open ? "/api/claude/installs" : null, [open]);
  const [scope, setScope] = React.useState<Scope>(root ? "project" : "global");
  return (
    <Dialog open={open} onOpenChange={(o) => onOpenChange(o)}>
      <DialogContent className="max-h-[85vh] overflow-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Import skills from Claude Code</DialogTitle>
          <DialogDescription>
            From <Mono>~/.claude/skills</Mono> on this machine and in each WSL distribution. The folder is copied whole — scripts and references too.
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2 text-sm">
          Import into
          <NativeSelect value={scope} onChange={(v) => setScope(v as Scope)} options={[...(root ? [{ value: "project", label: "this project" }] : []), { value: "global", label: "global skills" }]} />
        </div>
        {loading && !data && <div className="text-sm text-muted-foreground">Looking for installs…</div>}
        <ErrorNote error={error} />
        {(data ?? []).map((inst) => (
          <div key={inst.id} className="space-y-2">
            <div className="text-sm font-medium">
              {inst.label} <span className="font-mono text-xs font-normal text-muted-foreground">{inst.home}</span>
            </div>
            {inst.skills.length === 0 && <div className="text-xs text-muted-foreground">no skills</div>}
            {inst.skills.map((s) => (
              <div key={s.dir} className="flex items-start gap-3 rounded-lg border p-2.5">
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">{s.name}</div>
                  <div className="line-clamp-2 text-xs text-muted-foreground">{s.description}</div>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={async () => {
                    try {
                      await api.post("/api/skills/import", { install: inst.id, name: s.name, scope, root });
                      toast.success(`Imported ${s.name}`);
                    } catch (e) {
                      toast.error((e as Error).message);
                    }
                  }}
                >
                  Import
                </Button>
              </div>
            ))}
          </div>
        ))}
      </DialogContent>
    </Dialog>
  );
}
