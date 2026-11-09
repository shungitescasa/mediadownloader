const form = document.getElementById("form");
const input = document.getElementById("url");
const error = document.getElementById("error");
const list = document.getElementById("queue");

const ICONS = {
  video: "/static/img/icons/video.png",
  music: "/static/img/icons/music.png",
  download: "/static/img/icons/download.png",
  erase: "/static/img/icons/erase.png",
  stop: "/static/img/icons/stop.png",
};

const STATES = {
  pending: "waiting in queue",
  downloading: "downloading",
  done: "finished",
  failed: "failed",
  deleted: "deleted",
};

const entries = new Map();
const waiting = [];
let active = null;
let counter = 0;

function clockSkew(response) {
  const server = Date.parse(response.headers.get("Date"));
  return Number.isNaN(server) ? 0 : server - Date.now();
}

function formatLeft(ms) {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const hours = Math.floor(total / 3600);
  const minutes = String(Math.floor((total % 3600) / 60)).padStart(2, "0");
  const seconds = String(total % 60).padStart(2, "0");
  return hours > 0
    ? hours + ":" + minutes + ":" + seconds
    : minutes + ":" + seconds;
}

function showError(message) {
  error.textContent = message;
  error.hidden = !message;
}

function taskType() {
  return "video";
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) {
    node.className = className;
  }
  if (text !== undefined) {
    node.textContent = text;
  }
  return node;
}

function build(task, skew) {
  const item = element("div", "task");

  const icon = element("img");
  const type = taskType(task);
  icon.src = ICONS[type];
  icon.alt = type;
  icon.width = 16;
  icon.height = 16;

  const info = element("div", "info");
  const link = element("a", "link", task.url);
  link.href = task.url;
  link.title = task.url;
  link.target = "_blank";
  link.rel = "noopener noreferrer";
  const state = element("span", "state");
  info.append(link, state);

  const actions = element("div", "actions");

  item.append(icon, info, actions);
  return {
    id: task.id,
    item,
    state,
    actions,
    source: null,
    timer: null,
    deleted: false,
    expiresAt: Date.parse(task.expires_at),
    skew,
    url: task.url,
  };
}

function actionIcon(name) {
  const icon = element("img");
  icon.src = ICONS[name];
  icon.alt = name;
  icon.width = 16;
  icon.height = 16;
  return icon;
}

function setActions(entry, status) {
  entry.actions.textContent = "";

  if (status === "pending" || status === "downloading") {
    const stopButton = element("button");
    stopButton.type = "button";
    stopButton.title = "stop";
    stopButton.setAttribute("aria-label", "stop");
    stopButton.append(actionIcon("stop"));
    stopButton.addEventListener("click", () => stop(entry));
    entry.actions.append(stopButton);
  }

  if (status === "done") {
    const download = element("a");
    download.href = "/download?id=" + encodeURIComponent(entry.id);
    download.title = "download";
    download.setAttribute("aria-label", "download");
    download.append(actionIcon("download"));
    entry.actions.append(download);
  }

  const erase = element("button");
  erase.type = "button";
  erase.title = "erase";
  erase.setAttribute("aria-label", "erase");
  erase.append(actionIcon("erase"));
  erase.addEventListener("click", () => remove(entry));
  entry.actions.append(erase);
}

function stopTimer(entry) {
  if (entry.timer !== null) {
    clearInterval(entry.timer);
    entry.timer = null;
  }
}

function markDeleted(entry) {
  stopTimer(entry);
  entry.deleted = true;
  entry.item.className = "task deleted";
  entry.state.textContent = STATES.deleted;
  setActions(entry, null);
}

function tick(entry) {
  const left = entry.expiresAt - (Date.now() + entry.skew);
  if (Number.isNaN(left) || left <= 0) {
    markDeleted(entry);
    return;
  }
  entry.state.textContent =
    STATES.done + " \u00b7 deleted in " + formatLeft(left);
}

function startCountdown(entry) {
  if (entry.timer !== null) {
    return;
  }
  tick(entry);
  if (!entry.deleted) {
    entry.timer = setInterval(() => tick(entry), 1000);
  }
}

function render(entry, task) {
  if (entry.deleted) {
    return;
  }

  entry.item.className = "task " + task.status;
  entry.state.textContent = STATES[task.status] || task.status;
  setActions(entry, task.status);

  if (task.status === "done") {
    startCountdown(entry);
  }
}

function release(entry) {
  if (active === entry) {
    active = null;
    pump();
  }
}

function fail(entry, message) {
  stopTimer(entry);
  entry.item.className = "task failed";
  entry.state.textContent = message;
  setActions(entry, null);
}

function remove(entry) {

  const index = waiting.indexOf(entry);
  if (index >= 0) {
    waiting.splice(index, 1);
  }

  entry.removed = true;
  if (entry.source) {
    entry.source.close();
  }
  stopTimer(entry);
  entry.item.remove();
  if (entries.get(entry.id) === entry) {
    entries.delete(entry.id);
  }
  release(entry);
}

async function stop(entry) {
  const local = entry.id.startsWith("local-");
  const index = waiting.indexOf(entry);
  if (index >= 0) {
    waiting.splice(index, 1);
  }

  if (!local) {
    try {
      const response = await fetch(
        "/stop?id=" + encodeURIComponent(entry.id),
        { method: "DELETE" },
      );
      if (!response.ok && response.status !== 404) {
        showError("failed to stop the task");
        return;
      }
    } catch (err) {
      showError("failed to stop the task");
      return;
    }
  }

  entry.removed = true;
  entry.stopped = true;
  if (entry.source) {
    entry.source.close();
  }
  markDeleted(entry);
  release(entry);
}

function track(entry) {
  const source = new EventSource("/track?id=" + encodeURIComponent(entry.id));
  entry.source = source;

  source.onmessage = (event) => {
    const update = JSON.parse(event.data);
    render(entry, update);
    if (update.status === "done" || update.status === "failed") {
      source.close();
      release(entry);
    }
  };

  source.onerror = () => {
    if (source.readyState === EventSource.CLOSED) {
      source.close();
      if (!entry.removed) {
        fail(entry, "tracking lost");
      }
      release(entry);
    }
  };
}

async function start(entry) {
  console.log(entry);
  const data = new FormData();
  data.append("url", entry.url);

  let response;
  let body;
  try {
    response = await fetch("/queue", { method: "POST", body: data });
    body = await response.json();
  } catch (err) {
    console.log(err);
    if (!entry.removed) {
      fail(entry, "request failed");
    }
    release(entry);
    return;
  }

  if (entry.removed) {
    if (entry.stopped && response.status === 202 && body.data && body.data[0]) {
      fetch("/stop?id=" + encodeURIComponent(body.data[0].id), {
        method: "DELETE",
      }).catch(() => {});
    }
    return;
  }

  if (response.status !== 202 || !body.data || body.data.length === 0) {
    fail(entry, body.message || "request failed");
    release(entry);
    return;
  }

  const task = body.data[0];
  const existing = entries.get(task.id);
  if (existing && !existing.deleted) {
    entry.item.remove();
    entries.delete(entry.id);
    entry.removed = true;
    release(entry);
    return;
  }

  entries.delete(entry.id);
  entry.id = task.id;
  entry.expiresAt = Date.parse(task.expires_at);
  entry.skew = clockSkew(response);
  entries.set(entry.id, entry);

  render(entry, task);
  if (task.status === "done" || task.status === "failed") {
    release(entry);
    return;
  }
  track(entry);
}

function pump() {
  if (active !== null) {
    return;
  }

  const next = waiting.shift();
  if (!next) {
    return;
  }

  active = next;
  start(next);
}

function enqueue(url) {
  counter += 1;
  const entry = build({ id: "local-" + counter, url }, 0);
  entry.removed = false;
  entries.set(entry.id, entry);
  render(entry, { status: "pending" });
  list.prepend(entry.item);
  waiting.push(entry);
  pump();
}

form.addEventListener("submit", (event) => {
  event.preventDefault();
  showError("");

  const url = input.value.trim();
  if (!url) {
    return;
  }

  enqueue(url);
  form.reset();
  input.focus();
});
