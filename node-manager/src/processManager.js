const { spawn } = require("child_process");
const path = require("path");

const gatewayBinary = path.resolve(__dirname, "../../novaairouter.exe");
const cwd = path.resolve(__dirname, "../..");

let state = {
  running: false,
  pid: null,
  startedAt: null,
  args: [],
  lastExit: null,
  logs: []
};

let proc = null;

function pushLog(line) {
  state.logs.push(line);
  if (state.logs.length > 300) {
    state.logs = state.logs.slice(-300);
  }
}

function startGateway(args = []) {
  if (state.running) {
    throw new Error("novaairouter is already running");
  }

  proc = spawn(gatewayBinary, args, { cwd, windowsHide: true });
  state.running = true;
  state.pid = proc.pid;
  state.startedAt = new Date().toISOString();
  state.args = args;
  state.lastExit = null;
  pushLog(`[manager] started pid=${proc.pid} args=${args.join(" ")}`);

  proc.stdout.on("data", (buf) => pushLog(buf.toString().trimEnd()));
  proc.stderr.on("data", (buf) => pushLog(buf.toString().trimEnd()));

  proc.on("exit", (code, signal) => {
    state.running = false;
    state.pid = null;
    state.lastExit = { code, signal, at: new Date().toISOString() };
    pushLog(`[manager] exited code=${code} signal=${signal || ""}`.trim());
    proc = null;
  });

  return getStatus();
}

function stopGateway() {
  if (!state.running || !proc) {
    throw new Error("novaairouter is not running");
  }
  proc.kill();
  return { ok: true };
}

function restartGateway(args = null) {
  const currentArgs = Array.isArray(args) ? args : (state.args || []);
  if (state.running && proc) {
    proc.kill();
  }
  return startGateway(currentArgs);
}

function getStatus() {
  return {
    running: state.running,
    pid: state.pid,
    startedAt: state.startedAt,
    args: state.args,
    lastExit: state.lastExit
  };
}

function getLogs() {
  return state.logs;
}

module.exports = {
  startGateway,
  stopGateway,
  restartGateway,
  getStatus,
  getLogs
};
