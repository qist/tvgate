/**
 * Classic Worker 入口（`new Worker(url)` 无 type=module 的兜底路径）。
 *
 * 背景：主线程默认以 `new Worker(url, { type: "module" })` 创建转封装 worker。
 * module worker 需要 Chrome 80+/X5 较新内核；旧内核（部分小米 WebView、UC/X5、
 * Chrome<80）在该构造调用上直接抛 TypeError。Vite 默认以 IIFE 格式产出 worker
 * （见 dist 的 transmux-worker-*.js：无 import/export，本来就是 classic 脚本），
 * 所以同一份产物完全可以用 classic 语义加载。
 *
 * 本文件不做任何实现：`export *` 会先执行被导出的模块（其顶层副作用——
 * `self.onmessage` 注册、`post({type:"ready"})`——随之生效），Vite 再为这份
 * 组合单独产出一份 IIFE 副本。主线程在 module worker 创建失败时退到本入口重试
 * （见 worker-client.ts 的 ensureWorker），保证旧内核上 MSE 链路不因 worker 创建
 * 失败而整体瘫痪（否则表现为"黑屏、无错误提示"）。
 *
 * 注意：不得在此文件写顶层 await 等 IIFE 目标不支持的语法。
 */

export * from "./transmux-worker";
