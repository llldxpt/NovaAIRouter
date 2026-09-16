# Node Manager (lightweight)

本目录是一个基于 Node.js 的轻量管理器原型，放在 `novaairouter` 项目内。

## 目标

- 仅暴露基础参数（主页）+ 高级参数（折叠区），字段仍严格白名单（来源于 `example-config.yaml`）
- 管理 `novaairouter.exe` 启停
- 插件仅展示列表（只读，不允许编辑）
- 内嵌 novaairouter 原生前端页面（`/v1/webui`）

## 启动

```bash
cd node-manager
npm install
npm start
```

默认地址：`http://127.0.0.1:15048`

## 白名单参数

定义在 `src/config.js` 的 `ALLOWED_KEYS`：

- disable-admin-auth
- api-key
- log-level
- listen-addr
- discovery-addr
- default-max-concurrency
- queue-capacity
- backend-timeout
- queue-timeout
- heartbeat-timeout

超出白名单的参数不会被保存。
