const fs = require("fs");
const path = require("path");
const crypto = require("crypto");
const yaml = require("js-yaml");

const CONFIG_PATH = path.resolve(__dirname, "../../example-config.yaml");

const ALLOWED_KEYS = [
  "share-mode",
  "disable-admin-auth",
  "api-key",
  "log-level",
  "listen-addr",
  "discovery-addr",
  "default-max-concurrency",
  "queue-capacity",
  "backend-timeout",
  "queue-timeout",
  "heartbeat-timeout"
];

const MAIN_KEYS = [
  "auth-mode",
  "log-level",
  "default-max-concurrency",
  "queue-capacity"
];

const ADVANCED_KEYS = [
  "backend-timeout",
  "queue-timeout",
  "heartbeat-timeout"
];

const DEFAULTS = {
  "disable-admin-auth": true,
  "api-key": "",
  "log-level": "info",
  "listen-addr": ":15050",
  "discovery-addr": ":15052",
  "default-max-concurrency": 10,
  "queue-capacity": 1000,
  "backend-timeout": "300s",
  "queue-timeout": "30s",
  "heartbeat-timeout": "15s"
};

function deriveAccountApiKeyFromUser(user) {
  const userId = String(user.id ?? user.user_id ?? user.uid ?? "").trim();
  const username = String(user.username ?? user.name ?? user.nickname ?? "").trim();
  if (!userId && !username) {
    throw new Error("用户信息不完整，无法生成 API key");
  }
  const material = JSON.stringify({ userId, username });
  return crypto.createHash("sha256").update(material).digest("hex");
}

function fingerprintUser(user) {
  const userId = String(user?.id ?? user?.user_id ?? user?.uid ?? "").trim();
  const username = String(user?.username ?? user?.name ?? user?.nickname ?? "").trim();
  const material = JSON.stringify({ userId, username });
  return crypto.createHash("sha256").update(material).digest("hex");
}

function readRawConfig() {
  const text = fs.readFileSync(CONFIG_PATH, "utf8");
  return yaml.load(text) || {};
}

function getSafeConfig() {
  const raw = readRawConfig();
  const safe = {};
  for (const key of ALLOWED_KEYS) {
    safe[key] = raw[key] !== undefined ? raw[key] : DEFAULTS[key];
  }
  safe["listen-addr"] = ":15050";
  safe["discovery-addr"] = ":15052";
  safe["auth-mode"] = safe["share-mode"] || (safe["disable-admin-auth"] ? "public" : "account");
  return safe;
}

function validatePayload(payload) {
  const clean = {};
  for (const key of ALLOWED_KEYS) {
    if (payload[key] !== undefined) {
      clean[key] = payload[key];
    }
  }

  if (clean["default-max-concurrency"] !== undefined && Number(clean["default-max-concurrency"]) < 1) {
    throw new Error("default-max-concurrency must be >= 1");
  }
  if (clean["queue-capacity"] !== undefined && Number(clean["queue-capacity"]) < 1) {
    throw new Error("queue-capacity must be >= 1");
  }
  return clean;
}

function saveSafeConfig(partial, injectedUser = null) {
  const raw = readRawConfig();

  const next = { ...partial };
  delete next["api-key"];

  if (next["auth-mode"] !== undefined) {
    if (next["auth-mode"] === "public") {
      next["share-mode"] = "public";
      next["disable-admin-auth"] = true;
      next["api-key"] = "";
    } else if (next["auth-mode"] === "local") {
      next["share-mode"] = "local";
      next["disable-admin-auth"] = false;
    } else {
      next["share-mode"] = "account";
      next["disable-admin-auth"] = false;
    }
    delete next["auth-mode"];
  }

  const clean = validatePayload(next);
  const merged = { ...raw, ...clean };

  merged["listen-addr"] = ":15050";
  merged["discovery-addr"] = ":15052";

  if (merged["share-mode"] === "public") {
    merged["disable-admin-auth"] = true;
    merged["api-key"] = "";
  }
  if (merged["share-mode"] === "account") {
    if (!injectedUser || typeof injectedUser !== "object") {
      throw new Error("仅限账户模式需要通过 novalauncher 接口获取登录用户信息")
    }
    merged["api-key"] = deriveAccountApiKeyFromUser(injectedUser);
    merged["disable-admin-auth"] = false;
  }
  if (merged["share-mode"] === "local") {
    merged["disable-admin-auth"] = false;
  }
  if (!merged["share-mode"]) {
    merged["share-mode"] = merged["disable-admin-auth"] ? "public" : "account";
  }

  const output = yaml.dump(merged, { lineWidth: 120, noRefs: true });
  fs.writeFileSync(CONFIG_PATH, output, "utf8");
  return getSafeConfig();
}

function saveConfigWithUserRotation(user) {
  const raw = readRawConfig();
  const merged = { ...raw };
  merged["listen-addr"] = ":15050";
  merged["discovery-addr"] = ":15052";

  const shareMode = String(merged["share-mode"] || "").trim().toLowerCase();
  if (shareMode !== "account") {
    return { changed: false, reason: "share-mode is not account", config: getSafeConfig() };
  }

  const newApiKey = deriveAccountApiKeyFromUser(user);
  if (String(merged["api-key"] || "") === newApiKey) {
    return { changed: false, reason: "api key unchanged", config: getSafeConfig() };
  }

  merged["api-key"] = newApiKey;
  merged["disable-admin-auth"] = false;

  const output = yaml.dump(merged, { lineWidth: 120, noRefs: true });
  fs.writeFileSync(CONFIG_PATH, output, "utf8");
  return { changed: true, reason: "api key rotated", config: getSafeConfig() };
}

module.exports = {
  ALLOWED_KEYS,
  MAIN_KEYS,
  ADVANCED_KEYS,
  CONFIG_PATH,
  getSafeConfig,
  saveSafeConfig,
  fingerprintUser,
  saveConfigWithUserRotation
};
