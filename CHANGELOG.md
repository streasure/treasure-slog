# Changelog

treasure-slog 日志库的版本记录。首个基线 tag 前的早期历史见 git log。

## [Unreleased]

### Added

- 函数选项扩展面：`New(path,opts...)`/`NewFromConfig`/`WithWriter`/
  `WithHandlerDecorator`/`WithCoreDecorator`/`WithHooks`/`WithErrorHandler`、
  `Handler()` 访问器与 `Decorate` 通用装饰器（`4d0dee8`）
- Hook 重设计：`BeforeWrite`/`AfterWrite(*Event)` 移入 handler 链采样内侧，
  `HookFuncs` 零值适配器，`AddHook` 链重放保留 `With` 属性（`4d0dee8`）
- 零分配守护 `zero_alloc_test` 与计时器接线守护测试（`ea4fb9b`）
- 采样统计 `Dropped`/`Sampled` 与配置 `tick`（`804ba71`）

### Changed

- worker 消费循环改阻塞唤醒（notify 邮箱），消除 ~15% CPU 空转轮询（`eb8be47`）
- 采样器对齐 zapcore.Sampler 语义（per-(级别,消息) 计数 + tick 窗口重置），
  ERROR 及以上放行且不占采样计数（`804ba71`/`b0aa381`）
- 序列化优化：`appendJSONString` 表驱动整段拷贝 + 手写 RFC3339Nano +
  `logf` 无 `%` 跳过 Sprintf（sync -35% / async -32% / TMax -45%，`4d0dee8`）
- README 与 docs/benchmark.md 25 处文档-实现偏差修正、5 个失效计时器接线
  纠偏与异步 flush 写错误统一上报（`ea4fb9b`/`889bab2`）

### Removed

- 删除压缩功能（`c78628f`/`00560b1`）
