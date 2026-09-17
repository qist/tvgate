/**
 * media-engine 内部错误类型（本项目自研，clean-room）。
 *
 * 定位：只表达「引擎内部不该发生」的断言式失败（位流越界 / 参数非法 / 状态冲突）。
 * 与 `errors.ts` 的 PlayerError 分工明确：
 *   - `PlayerError` 是**对外契约**：带 code，可上报、可判定、可驱动播放器恢复策略；
 *   - `EngineError` 只在开发期暴露真实故障点（解析器遇到损坏数据、调用方参数写错），
 *     在到达播放器之前会被各模块转成 PlayerError，或按「该轨降级」就地处理。
 *
 * 设计选择：单类 + `kind` 字段，而不是为每种失败建一个子类——内部错误不需要被
 * catch 分支按类型区分，一个 kind 足够定位，也避免类爆炸。
 */
export type EngineErrorKind =
  /** 状态冲突：调用顺序/生命周期不对（如缓冲已释放仍继续读）。 */
  | "state"
  /** 参数非法：调用方传入了超出契约的值。 */
  | "argument"
  /** 位流损坏：解析到越界/不可能出现的语法元素。 */
  | "bitstream"
  /** 兜底：内部不变量被破坏（理论上不可达）。 */
  | "internal";

export class EngineError extends Error {
  readonly kind: EngineErrorKind;

  constructor(kind: EngineErrorKind, message: string) {
    super(message);
    this.name = "EngineError";
    this.kind = kind;
  }
}
