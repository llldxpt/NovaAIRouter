const pluginStore = {
  modelRouter: {
    enabled: true,
    params: {
      routeMode: "model",
      fallbackService: "",
      timeoutMs: 30000
    }
  }
};

function getPlugins() {
  return pluginStore;
}

function updatePlugin(name, patch) {
  if (!pluginStore[name]) {
    throw new Error(`unknown plugin: ${name}`);
  }
  pluginStore[name] = {
    ...pluginStore[name],
    ...patch,
    params: {
      ...pluginStore[name].params,
      ...(patch.params || {})
    }
  };
  return pluginStore[name];
}

module.exports = {
  getPlugins,
  updatePlugin
};
