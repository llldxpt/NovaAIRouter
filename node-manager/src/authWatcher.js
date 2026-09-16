const fs = require("fs");
const path = require("path");
const { fingerprintUser, saveConfigWithUserRotation } = require("./config");
const { getStatus, restartGateway } = require("./processManager");

const AUTH_ROTATE_STATE_PATH = path.resolve(__dirname, "../../config_user/router_auth_state.json");
const LAUNCHER_AUTH_API = "http://127.0.0.1:5050/api/auth/user";

const watcherState = {
  enabled: true,
  intervalMs: 4000,
  restartRouterOnRotate: true,
  lastFingerprint: "",
  timer: null,
  lastCheckAt: null,
  lastRotateAt: null,
  lastResult: "idle"
};

function loadWatcherState() {
  try {
    if (!fs.existsSync(AUTH_ROTATE_STATE_PATH)) return;
    const raw = JSON.parse(fs.readFileSync(AUTH_ROTATE_STATE_PATH, "utf8") || "{}");
    watcherState.lastFingerprint = String(raw.lastFingerprint || "");
    watcherState.lastResult = String(raw.lastResult || "idle");
    watcherState.lastRotateAt = raw.lastRotateAt || null;
  } catch {}
}

function persistWatcherState() {
  try {
    fs.mkdirSync(path.dirname(AUTH_ROTATE_STATE_PATH), { recursive: true });
    fs.writeFileSync(
      AUTH_ROTATE_STATE_PATH,
      JSON.stringify(
        {
          lastFingerprint: watcherState.lastFingerprint,
          lastResult: watcherState.lastResult,
          lastRotateAt: watcherState.lastRotateAt
        },
        null,
        2
      ),
      "utf8"
    );
  } catch {}
}

async function fetchLauncherUserFromApi() {
  const response = await fetch(LAUNCHER_AUTH_API, { method: "GET" });
  if (!response.ok) {
    throw new Error(`novalauncher auth api failed: HTTP ${response.status}`);
  }
  const data = await response.json();
  if (!data || data.loggedIn !== true || !data.user || typeof data.user !== "object") {
    throw new Error("novalauncher is not logged in");
  }
  return data.user;
}

async function evaluateAndRotate() {
  watcherState.lastCheckAt = new Date().toISOString();
  let user;
  try {
    user = await fetchLauncherUserFromApi();
  } catch (err) {
    watcherState.lastResult = `auth-unavailable: ${err.message}`;
    persistWatcherState();
    return;
  }

  const fp = fingerprintUser(user);
  if (fp === watcherState.lastFingerprint) {
    watcherState.lastResult = "no-change";
    persistWatcherState();
    return;
  }

  const rotate = saveConfigWithUserRotation(user);
  watcherState.lastFingerprint = fp;
  watcherState.lastResult = rotate.changed ? "rotated" : `skip: ${rotate.reason}`;

  if (rotate.changed) {
    watcherState.lastRotateAt = new Date().toISOString();
    const status = getStatus();
    if (watcherState.restartRouterOnRotate && status.running) {
      try {
        restartGateway(status.args || []);
        watcherState.lastResult = "rotated-and-restarted";
      } catch (err) {
        watcherState.lastResult = `rotated-restart-failed: ${err.message}`;
      }
    }
  }

  persistWatcherState();
}

function startAuthRotationWatcher() {
  if (watcherState.timer || !watcherState.enabled) return;
  loadWatcherState();
  evaluateAndRotate();
  watcherState.timer = setInterval(() => {
    evaluateAndRotate();
  }, watcherState.intervalMs);
}

function getAuthRotationStatus() {
  return {
    enabled: watcherState.enabled,
    intervalMs: watcherState.intervalMs,
    restartRouterOnRotate: watcherState.restartRouterOnRotate,
    lastFingerprint: watcherState.lastFingerprint ? "set" : "",
    lastCheckAt: watcherState.lastCheckAt,
    lastRotateAt: watcherState.lastRotateAt,
    lastResult: watcherState.lastResult
  };
}

module.exports = {
  startAuthRotationWatcher,
  getAuthRotationStatus
};
