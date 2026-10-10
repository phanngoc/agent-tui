// The popup: whether the extension is connected (or the code to approve),
// and sharing the tab in front of you with the agent.

const $ = (id) => document.getElementById(id);

async function render() {
  const { status = { state: "offline" } } = await chrome.storage.session.get("status");
  const { tabs = [] } = await chrome.storage.session.get("tabs");
  const s = $("status");
  if (status.state === "ok") s.innerHTML = '<span class="dot" style="background:#16a34a"></span>Connected to agent-tui';
  else if (status.state === "pending")
    s.innerHTML =
      '<span class="dot" style="background:#d97706"></span>Waiting for approval. On the agent-tui admin\'s <b>Browser</b> page, approve this code:<div class="code">' +
      status.code +
      "</div>";
  else s.innerHTML = '<span class="dot" style="background:#dc2626"></span>Not connected' + (status.error ? '<div class="muted">' + status.error + "</div>" : "") + '<div class="muted">Is the gateway running? <code>agent-tui gateway start</code></div>';

  const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
  const t = $("tab");
  t.innerHTML = "";
  if (!active || !/^https?:/.test(active.url || "")) return;
  const shared = tabs.includes(active.id);
  const b = document.createElement("button");
  b.className = shared ? "" : "primary";
  b.textContent = shared ? "Stop sharing this tab" : "Let agent-tui use this tab";
  b.onclick = async () => {
    await chrome.runtime.sendMessage({ type: shared ? "unshare" : "share", tab: active.id });
    render();
  };
  t.appendChild(b);
  const note = document.createElement("div");
  note.className = "muted";
  note.textContent = shared ? "The agent can read and act in this tab." : "The agent only touches the tabs it opens and the ones you share.";
  t.appendChild(note);
}

(async () => {
  const { gateway } = await chrome.storage.local.get("gateway");
  const g = $("gateway");
  g.value = gateway || "";
  g.placeholder = "default: the gateway that made this folder";
  g.onchange = async () => {
    await chrome.storage.local.set({ gateway: g.value.trim() });
    await chrome.storage.local.remove("token");
    chrome.runtime.sendMessage({ type: "kick" });
  };
  chrome.runtime.sendMessage({ type: "kick" });
  render();
  setInterval(render, 1500);
})();
