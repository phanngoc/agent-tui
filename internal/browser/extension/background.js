// agent-tui's hand in Chrome. It asks the gateway for work (a long poll),
// does it in the tabs it may touch, and posts back what happened.
//
// The tabs it may touch: the ones it opened itself, kept in an "agent-tui"
// tab group, and the ones the user shared from the popup. Nothing else.

// self.AGENT_TUI = { gateway }, written by the gateway that made this folder.
// Loaded from the repository instead, there is none: the default port it is.
try {
  importScripts("config.js");
} catch {}

const SNAPSHOT_CHARS = 15000;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function settings() {
  const s = await chrome.storage.local.get(["client", "token", "gateway"]);
  if (!s.client) {
    s.client = crypto.randomUUID();
    await chrome.storage.local.set({ client: s.client });
  }
  s.gateway = (s.gateway || (self.AGENT_TUI && self.AGENT_TUI.gateway) || "http://127.0.0.1:7788").replace(/\/$/, "");
  return s;
}

async function setStatus(status) {
  await chrome.storage.session.set({ status });
  chrome.action.setBadgeText({ text: status.state === "ok" ? "" : status.state === "pending" ? "?" : "!" });
  chrome.action.setBadgeBackgroundColor({ color: status.state === "pending" ? "#d97706" : "#dc2626" });
}

async function call(path, body) {
  const s = await settings();
  const r = await fetch(s.gateway + "/api/browser/ext/" + path, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Agent-Tui-Client": s.client, "X-Agent-Tui-Token": s.token || "" },
    body: JSON.stringify(body || {}),
  });
  if (r.status === 401) {
    await chrome.storage.local.remove("token");
    throw new Error("unauthorized");
  }
  if (!r.ok) throw new Error(path + ": http " + r.status);
  return r.status === 204 ? {} : r.json();
}

// hello says it is here, and learns whether it may work.
async function hello() {
  const r = await call("hello", { name: navigator.userAgent.match(/(Chrome|Chromium|Edg)\/[\d.]+/)?.[0] || "Chrome", version: chrome.runtime.getManifest().version });
  if (r.token) await chrome.storage.local.set({ token: r.token });
  await setStatus({ state: r.status, code: r.code || "", at: Date.now() });
  return r.status === "ok";
}

let looping = false;
async function loop() {
  if (looping) return;
  looping = true;
  try {
    for (;;) {
      try {
        if (!(await hello())) {
          await sleep(3000);
          continue;
        }
        for (;;) {
          const r = await call("next", {});
          if (r.command) handle(r.command); // in parallel: the poll goes on
        }
      } catch (e) {
        await setStatus({ state: "offline", error: String(e.message || e), at: Date.now() });
        await sleep(4000);
      }
    }
  } finally {
    looping = false;
  }
}

async function handle(cmd) {
  let res;
  try {
    const fn = ACTIONS[cmd.action];
    if (!fn) throw new Error("no action " + cmd.action);
    res = { id: cmd.id, ok: true, data: await fn(cmd.args || {}) };
  } catch (e) {
    res = { id: cmd.id, ok: false, error: String(e.message || e) };
  }
  try {
    await call("result", res);
  } catch {
    /* the gateway times the call out */
  }
}

// The tabs the agent may touch, kept for the browser's session.
async function allowed() {
  const { tabs = [], current = null } = await chrome.storage.session.get(["tabs", "current"]);
  const live = [];
  for (const id of tabs) {
    try {
      await chrome.tabs.get(id);
      live.push(id);
    } catch {}
  }
  return { tabs: live, current: live.includes(current) ? current : live[live.length - 1] ?? null };
}

async function allow(id, makeCurrent = true) {
  const a = await allowed();
  if (!a.tabs.includes(id)) a.tabs.push(id);
  await chrome.storage.session.set({ tabs: a.tabs, current: makeCurrent ? id : a.current });
}

async function tabFor(args) {
  const a = await allowed();
  const id = args.tab ?? a.current;
  if (id == null) throw new Error("no tab yet: open one with browser_open, or share a tab from the agent-tui extension's popup");
  if (!a.tabs.includes(id)) throw new Error("tab " + id + " is not shared with agent-tui: open one with browser_open, or share it from the extension's popup");
  await chrome.storage.session.set({ current: id });
  return id;
}

// targetWindow is the window a new tab goes in. A service worker has no
// "current window" — with Chrome in the background, chrome.tabs.create
// without one fails — so it names one: the last focused, or any.
async function targetWindow() {
  try {
    const w = await chrome.windows.getLastFocused({ windowTypes: ["normal"] });
    if (w && w.id != null) return w.id;
  } catch {}
  const all = await chrome.windows.getAll({ windowTypes: ["normal"] });
  return all.length ? all[0].id : null;
}

async function group(tabId) {
  try {
    const { groupId } = await chrome.storage.session.get("groupId");
    let g = groupId;
    if (g != null) {
      try {
        await chrome.tabGroups.get(g);
      } catch {
        g = null;
      }
    }
    if (g == null) {
      g = await chrome.tabs.group({ tabIds: [tabId] });
      await chrome.tabGroups.update(g, { title: "agent-tui", color: "purple" });
      await chrome.storage.session.set({ groupId: g });
    } else {
      await chrome.tabs.group({ groupId: g, tabIds: [tabId] });
    }
  } catch {
    /* a tab in another window, or groups unavailable: it still works */
  }
}

function settled(tabId, timeout = 15000) {
  return new Promise((resolve) => {
    let done = false;
    const finish = () => {
      if (done) return;
      done = true;
      chrome.tabs.onUpdated.removeListener(on);
      clearTimeout(t);
      setTimeout(resolve, 300);
    };
    const on = (id, info) => id === tabId && info.status === "complete" && finish();
    const t = setTimeout(finish, timeout);
    chrome.tabs.onUpdated.addListener(on);
    chrome.tabs.get(tabId).then((tab) => tab.status === "complete" && setTimeout(() => chrome.tabs.get(tabId).then((t2) => t2.status === "complete" && finish()), 400), finish);
  });
}

async function run(tabId, func, args = []) {
  const [r] = await chrome.scripting.executeScript({ target: { tabId }, func, args });
  if (r && r.result && r.result.error) throw new Error(r.result.error);
  return r ? r.result : undefined;
}

async function describe(tabId) {
  const t = await chrome.tabs.get(tabId);
  return { tab: tabId, url: t.url, title: t.title };
}

const ACTIONS = {
  async tabs() {
    const a = await allowed();
    const out = [];
    for (const id of a.tabs) {
      const t = await chrome.tabs.get(id);
      out.push({ tab: id, url: t.url, title: t.title, current: id === a.current });
    }
    return { tabs: out };
  },

  async open({ url, newTab = true }) {
    if (!/^https?:\/\//i.test(url || "")) throw new Error("open takes an http(s) address");
    let id;
    const a = await allowed();
    if (!newTab && a.current != null) {
      id = a.current;
      await chrome.tabs.update(id, { url, active: true });
    } else {
      const windowId = await targetWindow();
      if (windowId == null) {
        // No window at all: one of its own.
        const w = await chrome.windows.create({ url, focused: true });
        id = w.tabs[0].id;
      } else {
        const t = await chrome.tabs.create({ url, active: true, windowId });
        id = t.id;
      }
      await group(id);
    }
    await allow(id);
    await settled(id);
    return describe(id);
  },

  async navigate(args) {
    const id = await tabFor(args);
    if (args.url) {
      if (!/^https?:\/\//i.test(args.url)) throw new Error("navigate takes an http(s) address");
      await chrome.tabs.update(id, { url: args.url });
    } else if (args.to === "back" || args.to === "forward") {
      // chrome.tabs.goBack misses history a single-page app pushed itself;
      // the page's own history does not.
      try {
        await (args.to === "back" ? chrome.tabs.goBack(id) : chrome.tabs.goForward(id));
      } catch {
        await run(id, (to) => (to === "back" ? history.back() : history.forward()), [args.to]);
        await sleep(500);
      }
    } else await chrome.tabs.reload(id);
    await settled(id);
    return describe(id);
  },

  async snapshot(args) {
    const id = await tabFor(args);
    const page = await run(id, pageSnapshot, [args.max || SNAPSHOT_CHARS]);
    return { ...(await describe(id)), ...page };
  },

  async click(args) {
    const id = await tabFor(args);
    const before = (await chrome.tabs.get(id)).url;
    const r = await run(id, clickRef, [String(args.ref)]);
    await sleep(400);
    const t = await chrome.tabs.get(id);
    if (t.status === "loading" || t.url !== before) await settled(id);
    return { ...r, ...(await describe(id)) };
  },

  async type(args) {
    const id = await tabFor(args);
    const r = await run(id, typeRef, [String(args.ref), String(args.text ?? ""), !!args.clear, !!args.submit]);
    if (args.submit) {
      await sleep(400);
      await settled(id, 10000);
    }
    return { ...r, ...(await describe(id)) };
  },

  async key(args) {
    const id = await tabFor(args);
    const r = await run(id, pressKey, [String(args.key || "Enter")]);
    await sleep(300);
    return { ...r, ...(await describe(id)) };
  },

  async scroll(args) {
    const id = await tabFor(args);
    return { ...(await run(id, scrollPage, [String(args.direction || "down"), Number(args.amount) || 0])), ...(await describe(id)) };
  },

  async screenshot(args) {
    const id = await tabFor(args);
    const t = await chrome.tabs.get(id);
    await chrome.tabs.update(id, { active: true });
    await chrome.windows.update(t.windowId, { focused: true }).catch(() => {});
    await sleep(250);
    const dataUrl = await chrome.tabs.captureVisibleTab(t.windowId, { format: "png" });
    return { ...(await describe(id)), dataUrl };
  },

  async close(args) {
    const id = await tabFor(args);
    await chrome.tabs.remove(id);
    return { closed: id };
  },
};

// ---- run in the page: each function is self-contained ----

function pageSnapshot(max) {
  document.querySelectorAll("[data-agent-ref]").forEach((e) => e.removeAttribute("data-agent-ref"));
  const sel =
    'a[href],button,input:not([type=hidden]),select,textarea,summary,[role=button],[role=link],[role=checkbox],[role=radio],[role=tab],[role=menuitem],[role=option],[role=switch],[role=combobox],[role=textbox],[contenteditable=""],[contenteditable="true"]';
  const shown = (el) => {
    const r = el.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) return false;
    const s = getComputedStyle(el);
    return s.visibility !== "hidden" && s.display !== "none" && s.opacity !== "0";
  };
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  // A text box is named by its label or placeholder, never by what's typed
  // in it — that's its value="", and the name has to stay put across typing.
  const typed = (el) =>
    el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable || (el.tagName === "INPUT" && kind(el) === "textbox");
  const name = (el) =>
    clean(
      el.getAttribute("aria-label") ||
        (el.labels && el.labels[0] && el.labels[0].innerText) ||
        (typed(el) ? "" : el.innerText || el.value) ||
        el.getAttribute("placeholder") ||
        el.getAttribute("title") ||
        el.getAttribute("alt") ||
        (el.querySelector && el.querySelector("img[alt]") && el.querySelector("img[alt]").alt) ||
        el.getAttribute("name") ||
        "",
    ).slice(0, 90);
  const kind = (el) => {
    const r = el.getAttribute("role");
    if (r) return r;
    const t = el.tagName.toLowerCase();
    if (t === "a") return "link";
    if (t === "input") {
      const ty = (el.getAttribute("type") || "text").toLowerCase();
      return ["checkbox", "radio", "submit", "button", "reset", "file", "range", "color"].includes(ty) ? ty : "textbox";
    }
    if (t === "textarea" || el.isContentEditable) return "textbox";
    return t;
  };
  const items = [];
  let n = 0;
  for (const el of document.querySelectorAll(sel)) {
    if (!shown(el) || el.disabled) continue;
    n += 1;
    el.setAttribute("data-agent-ref", String(n));
    const k = kind(el);
    let line = `[${n}] ${k} "${name(el)}"`;
    if (k === "link") line += " → " + (el.getAttribute("href") || "").slice(0, 120);
    if (k === "textbox" || el.tagName === "SELECT") line += ` value="${clean(el.isContentEditable ? el.innerText : el.value).slice(0, 60)}"`;
    if (k === "checkbox" || k === "radio") line += el.checked ? " checked" : " unchecked";
    const r = el.getBoundingClientRect();
    if (r.bottom < 0 || r.top > innerHeight) line += " (off screen)";
    items.push(line);
    if (items.length >= 400) break;
  }
  let text = (document.body ? document.body.innerText : "").replace(/\n{3,}/g, "\n\n").trim();
  const cut = text.length > max;
  if (cut) text = text.slice(0, max);
  return {
    text,
    truncated: cut,
    elements: items.join("\n"),
    scroll: { y: Math.round(scrollY), height: document.documentElement.scrollHeight, viewport: innerHeight },
  };
}

function clickRef(ref) {
  const el = document.querySelector(`[data-agent-ref="${ref}"]`);
  if (!el) return { error: `no element [${ref}]: take a new snapshot, the page has changed` };
  el.scrollIntoView({ block: "center", inline: "center" });
  const r = el.getBoundingClientRect();
  const at = { bubbles: true, cancelable: true, view: window, clientX: r.left + r.width / 2, clientY: r.top + r.height / 2 };
  el.dispatchEvent(new PointerEvent("pointerdown", at));
  el.dispatchEvent(new MouseEvent("mousedown", at));
  if (el.focus) el.focus();
  el.dispatchEvent(new PointerEvent("pointerup", at));
  el.dispatchEvent(new MouseEvent("mouseup", at));
  el.click();
  return { clicked: ref };
}

function typeRef(ref, text, clear, submit) {
  const el = document.querySelector(`[data-agent-ref="${ref}"]`);
  if (!el) return { error: `no element [${ref}]: take a new snapshot, the page has changed` };
  el.scrollIntoView({ block: "center" });
  el.focus();
  if (el.tagName === "SELECT") {
    const opt = [...el.options].find((o) => o.value === text || o.text.trim() === text);
    if (!opt) return { error: `no option "${text}" in [${ref}]` };
    el.value = opt.value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
    return { selected: opt.text };
  }
  if (el.isContentEditable) {
    if (clear) {
      document.execCommand("selectAll", false);
      document.execCommand("delete", false);
    }
    document.execCommand("insertText", false, text);
  } else {
    const proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    const set = Object.getOwnPropertyDescriptor(proto, "value").set;
    set.call(el, (clear ? "" : el.value) + text);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
  }
  if (submit) {
    const opts = { key: "Enter", code: "Enter", keyCode: 13, which: 13, bubbles: true, cancelable: true };
    const go = el.dispatchEvent(new KeyboardEvent("keydown", opts));
    el.dispatchEvent(new KeyboardEvent("keypress", opts));
    el.dispatchEvent(new KeyboardEvent("keyup", opts));
    if (go && el.form) el.form.requestSubmit ? el.form.requestSubmit() : el.form.submit();
  }
  return { typed: text.length, submitted: !!submit };
}

function pressKey(key) {
  const el = document.activeElement || document.body;
  const codes = { Enter: 13, Tab: 9, Escape: 27, ArrowDown: 40, ArrowUp: 38, ArrowLeft: 37, ArrowRight: 39, Backspace: 8, Space: 32, PageDown: 34, PageUp: 33 };
  const opts = { key: key === "Space" ? " " : key, code: key, keyCode: codes[key] || 0, which: codes[key] || 0, bubbles: true, cancelable: true };
  const go = el.dispatchEvent(new KeyboardEvent("keydown", opts));
  el.dispatchEvent(new KeyboardEvent("keyup", opts));
  if (go && key === "Enter" && el.form) el.form.requestSubmit ? el.form.requestSubmit() : el.form.submit();
  if (go && key === "PageDown") scrollBy(0, innerHeight * 0.9);
  if (go && key === "PageUp") scrollBy(0, -innerHeight * 0.9);
  return { pressed: key, on: el.tagName.toLowerCase() };
}

function scrollPage(direction, amount) {
  const d = amount || Math.round(innerHeight * 0.8);
  if (direction === "top") scrollTo(0, 0);
  else if (direction === "bottom") scrollTo(0, document.documentElement.scrollHeight);
  else scrollBy(0, direction === "up" ? -d : d);
  return { y: Math.round(scrollY), height: document.documentElement.scrollHeight };
}

// ---- keep the loop going ----

chrome.runtime.onInstalled.addListener(() => loop());
chrome.runtime.onStartup.addListener(() => loop());
chrome.alarms.create("agent-tui", { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener(() => loop());
chrome.runtime.onMessage.addListener((msg, _sender, reply) => {
  if (msg.type === "share") allow(msg.tab).then(() => reply({ ok: true }));
  else if (msg.type === "unshare")
    allowed().then(async (a) => {
      await chrome.storage.session.set({ tabs: a.tabs.filter((t) => t !== msg.tab) });
      reply({ ok: true });
    });
  else if (msg.type === "kick") loop().catch(() => {}), reply({ ok: true });
  return true;
});
loop();
