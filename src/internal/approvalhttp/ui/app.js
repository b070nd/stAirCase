// The review page of one run: it lists the proposals waiting for a person
// and sends approve or reject through the run's approval API. The token comes
// from the link's fragment, which browsers never send to a server. Agent text
// is only ever set as text, never as HTML.
"use strict";

const token = new URLSearchParams(location.hash.slice(1)).get("token") || sessionStorage.getItem("staircase-token") || "";
if (token) {
  sessionStorage.setItem("staircase-token", token);
  history.replaceState(null, "", location.pathname); // the token leaves the address bar
}
const list = document.getElementById("yields");
const status = document.getElementById("status");
const shown = new Map(); // yield id → its card

function api(path, init = {}) {
  init.headers = { ...(init.headers || {}), Authorization: "Bearer " + token };
  return fetch(path, init);
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function describe(req) {
  if (req.action_type === "shell_exec") return "wants to run a command";
  if (req.action_type === "final_review") return "asks you to approve the whole change before it is committed";
  if (req.review_after) return "has already made this change: approve keeps it, reject leaves it out";
  return "proposes a change";
}

function change(card, e, req) {
  if (req.action_type === "shell_exec") {
    card.append(el("div", "file", "in " + (e.file || ".")), el("pre", "cmd", e.replace_block));
    return;
  }
  card.append(el("div", "file", e.file));
  const before = (req.before || {})[e.file];
  if (before !== undefined && (e.search_block === "(new file)" || e.search_block === "(final content)")) {
    card.append(diff(before, e.replace_block));
    return;
  }
  switch (e.search_block) {
    case "(new file)":
    case "(final content)":
      card.append(el("pre", "add", e.replace_block));
      break;
    case "(delete file)":
      card.append(el("pre", "del", "the file is deleted"));
      break;
    default:
      card.append(el("pre", "del", e.search_block), el("pre", "add", e.replace_block));
  }
}

// diff shows how a file's lines change: removed lines red, added green, and
// unchanged ones around them (longer unchanged runs are folded).
function diff(before, after) {
  const lines = (s) => (s.endsWith("\n") ? s.slice(0, -1) : s).split("\n");
  const a = lines(before), b = lines(after);
  const n = a.length, m = b.length;
  const lcs = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
  const ops = [];
  let i = 0, j = 0;
  while (i < n || j < m) {
    if (i < n && j < m && a[i] === b[j]) { ops.push([" ", a[i]]); i++; j++; }
    else if (i < n && (j === m || lcs[i + 1][j] >= lcs[i][j + 1])) { ops.push(["-", a[i]]); i++; } // removals first
    else { ops.push(["+", b[j]]); j++; }
  }
  const box = el("div", "diff");
  let run = [];
  const flush = (last) => {
    const keep = 3;
    if (run.length > keep * 2 + 1) {
      const head = box.childElementCount ? run.slice(0, keep) : [];
      const tail = last ? [] : run.slice(-keep);
      for (const l of head) box.append(el("pre", "ctx", "  " + l));
      box.append(el("pre", "fold", "  … " + (run.length - head.length - tail.length) + " unchanged line(s)"));
      for (const l of tail) box.append(el("pre", "ctx", "  " + l));
    } else {
      for (const l of run) box.append(el("pre", "ctx", "  " + l));
    }
    run = [];
  };
  for (const [op, line] of ops) {
    if (op === " ") { run.push(line); continue; }
    flush(false);
    box.append(el("pre", op === "+" ? "add" : "del", op + " " + line));
  }
  flush(true);
  return box;
}

function render(y) {
  const req = y.request;
  const card = el("article");
  card.append(el("h2", "", (req.agent_name || "agent") + " " + describe(req)));
  card.append(el("p", "meta", "waiting since " + new Date(y.created).toLocaleTimeString()));
  for (const [label, text] of [["CHECK: ", req.guard], ["Drift: ", req.drift], ["Review: ", req.review]]) {
    if (text) card.append(el("div", "note", label + text));
  }
  if (req.reasoning_trace) {
    const d = el("details");
    d.append(el("summary", "", "The agent's explanation (it may be wrong or misleading)"), el("p", "", req.reasoning_trace));
    card.append(d);
  }
  for (const e of req.proposed_edits || []) change(card, e, req);

  const actions = el("div", "actions");
  const feedback = el("input");
  feedback.placeholder = "Feedback for the agent (optional)";
  feedback.setAttribute("aria-label", "Feedback for the agent");
  const approve = el("button", "approve", "Approve");
  const reject = el("button", "reject", "Reject");
  const decide = async (verb) => {
    approve.disabled = reject.disabled = true;
    const r = await api("/v1/yields/" + encodeURIComponent(y.id) + "/" + verb, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ feedback: feedback.value || verb + "d in the review page" }),
    });
    if (r.ok || r.status === 409 || r.status === 404) {
      card.remove();
      shown.delete(y.id);
      if (shown.size === 0) empty();
    } else {
      approve.disabled = reject.disabled = false;
      status.textContent = "Could not send the decision (" + r.status + ").";
    }
  };
  approve.addEventListener("click", () => decide("approve"));
  reject.addEventListener("click", () => decide("reject"));
  actions.append(feedback, approve, reject);
  card.append(actions);
  return card;
}

function empty() {
  if (!list.querySelector("article")) list.replaceChildren(document.getElementById("empty").content.cloneNode(true));
}

async function poll() {
  if (!token) {
    status.textContent = "Open the link the run printed: it carries the key to this run.";
    return;
  }
  try {
    const r = await api("/v1/yields");
    if (r.status === 401) {
      status.textContent = "This link's key is not valid for this run.";
      return;
    }
    const yields = (await r.json()).sort((a, b) => Number(a.id) - Number(b.id));
    status.textContent = yields.length ? yields.length + " waiting for you" : "Connected";
    const ids = new Set(yields.map((y) => y.id));
    for (const [id, card] of shown) if (!ids.has(id)) { card.remove(); shown.delete(id); }
    for (const y of yields) {
      if (shown.has(y.id)) continue; // keep what you are typing
      if (!list.querySelector("article")) list.replaceChildren();
      const card = render(y);
      shown.set(y.id, card);
      list.append(card);
    }
    if (!yields.length) empty();
  } catch {
    status.textContent = "The run has ended or cannot be reached.";
  }
  setTimeout(poll, 1500);
}

empty();
poll();
