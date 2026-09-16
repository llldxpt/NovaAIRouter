const express = require("express");
const path = require("path");
const { ALLOWED_KEYS, MAIN_KEYS, ADVANCED_KEYS, getSafeConfig, saveSafeConfig } = require("./config");
const { startGateway, stopGateway, restartGateway, getStatus } = require("./processManager");
const { startAuthRotationWatcher, getAuthRotationStatus } = require("./authWatcher");

async function fetchLauncherUserFromApi() {
  const response = await fetch("http://127.0.0.1:5050/api/auth/user", { method: "GET" });
  if (!response.ok) {
    throw new Error(`novalauncher auth api failed: HTTP ${response.status}`);
  }
  const data = await response.json();
  if (!data || data.loggedIn !== true || !data.user || typeof data.user !== "object") {
    throw new Error("请先在 novalauncher 中登录账户");
  }
  return data.user;
}

const app = express();

const PORT = 15048;
const ADMIN_BASE = "http://127.0.0.1:15049";

let pendingRestart = false;
let pendingRestartReason = "";

app.use(express.json({ limit: "1mb" }));
app.use(express.static(path.resolve(__dirname, "../web")));

app.get("/api/meta", (_req, res) => {
  res.json({
    allowedConfigKeys: ALLOWED_KEYS,
    mainConfigKeys: MAIN_KEYS,
    advancedConfigKeys: ADVANCED_KEYS,
    adminBase: ADMIN_BASE
  });
});

app.get("/api/config", (_req, res) => {
  res.json({ config: getSafeConfig() });
});

app.post("/api/config", async (req, res) => {
  try {
    let injectedUser = null;
    const mode = String(req.body?.["auth-mode"] || "").trim().toLowerCase();
    if (mode === "account") {
      injectedUser = await fetchLauncherUserFromApi();
    }

    const before = getSafeConfig();
    const saved = saveSafeConfig(req.body || {}, injectedUser);
    const changed = JSON.stringify(before) !== JSON.stringify(saved);

    if (changed) {
      pendingRestart = true;
      pendingRestartReason = "参数已更新，重启路由后生效";
    }

    res.json({
      ok: true,
      config: saved,
      restartRequired: changed,
      pendingRestart,
      pendingRestartReason
    });
  } catch (err) {
    res.status(400).json({ ok: false, error: err.message });
  }
});

app.get("/api/router/status", (_req, res) => {
  const status = getStatus();
  const runningText = status.running ? "运行中" : "未运行";
  const mode = getSafeConfig()["auth-mode"] || "public";
  res.json({
    status,
    userView: {
      running: status.running,
      runningText,
      mode,
      modeText: mode === "account" ? "仅限账户" : mode === "local" ? "仅限本机" : "公开共享",
      restartRequired: pendingRestart,
      restartReason: pendingRestartReason
    }
  });
});

app.post("/api/router/start", (req, res) => {
  try {
    const args = Array.isArray(req.body?.args) ? req.body.args : [];
    const status = startGateway(args);
    res.json({ ok: true, status });
  } catch (err) {
    res.status(409).json({ ok: false, error: err.message });
  }
});

app.post("/api/router/stop", (_req, res) => {
  try {
    stopGateway();
    res.json({ ok: true });
  } catch (err) {
    res.status(409).json({ ok: false, error: err.message });
  }
});

app.post("/api/router/restart", async (req, res) => {
  try {
    const force = req.body?.confirm === true;
    if (!force) {
      res.status(400).json({ ok: false, error: "需要用户确认后才能重启" });
      return;
    }

    const args = Array.isArray(req.body?.args) ? req.body.args : null;
    const status = restartGateway(args);
    pendingRestart = false;
    pendingRestartReason = "";
    res.json({ ok: true, status, pendingRestart, pendingRestartReason });
  } catch (err) {
    res.status(409).json({ ok: false, error: err.message });
  }
});

app.get("/api/router/restart-required", (_req, res) => {
  res.json({
    pendingRestart,
    reason: pendingRestartReason
  });
});

app.get("/api/router/page-url", (_req, res) => {
  res.json({ url: `${ADMIN_BASE}/v1/webui` });
});

app.get("/api/plugins", async (_req, res) => {
  try {
    const response = await fetch(`${ADMIN_BASE}/v1/global`);
    if (!response.ok) {
      const text = await response.text();
      res.status(response.status).json({ ok: false, error: text || "failed to fetch global data" });
      return;
    }
    const data = await response.json();
    const nodes = Array.isArray(data.nodes) ? data.nodes : [];
    const pluginMap = new Map();

    for (const node of nodes) {
      const nodeId = node.node_id || "unknown-node";
      const pathInfos = Array.isArray(node.path_infos) ? node.path_infos : [];
      for (const info of pathInfos) {
        if (!info || info.plugin !== true) continue;
        const key = `${info.path || ""}::${info.service_id || ""}`;
        if (!pluginMap.has(key)) {
          pluginMap.set(key, {
            name: info.description || info.path || "插件服务",
            healthy: info.healthy !== false,
            nodes: []
          });
        }
        pluginMap.get(key).nodes.push(nodeId);
      }
    }

    const plugins = Array.from(pluginMap.values()).map((p) => ({
      name: p.name,
      nodesCount: p.nodes.length,
      statusText: p.healthy ? "可用" : "异常",
      healthy: p.healthy
    }));

    res.json({ plugins });
  } catch (err) {
    res.status(502).json({ ok: false, error: `admin api unavailable: ${err.message}` });
  }
});

app.get("*", (_req, res) => {
  res.sendFile(path.resolve(__dirname, "../web/index.html"));
});

app.listen(PORT, () => {
  try {
    const st = getStatus();
    if (!st.running) {
      startGateway([]);
    }
  } catch (err) {
    console.error(`[node-manager] auto start router failed: ${err.message}`);
  }
  startAuthRotationWatcher();
  console.log(`[node-manager] listening on http://127.0.0.1:${PORT}`);
});
