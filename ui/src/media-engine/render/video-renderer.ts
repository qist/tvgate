/**
 * WebGL2 视频渲染（clean-room 实现）。
 * 设计 §5.10 行为：从 <video> 经 requestVideoFrameCallback 拉解码帧 → source stage
 * （去隔行或直通）→ enhancement（mosquito 降噪，须在源分辨率跑，避免放大压缩斑）→
 * present（上采样到 canvas）。门限：仅 SD/HD（≤1920×1088）走 WebGL；上采样上限 3840×2160；
 * 更大帧走直通/原生。
 *
 * ⚠️ 实现取舍（务必知悉）：本模块为**轻量单通路**实现——去隔行为「场内插值混合」，
 * 上采样为 Catmull-Rom（双三次）4x4 抽样；上游的 bwdif 与 FSR1(EASU+RCAS) 未实现，
 * 因其为复杂专用算法/受专利与实现细节约束，且目标设备（低端 TV）上重后处理本身
 * 就是卡顿与画质问题的来源（见长期记忆「显示垃圾根因」）。
 * 后续如需 bwdif/FSR，应作为独立 shader stage 接入本框架，而非伪造实现。
 */

export interface VideoRendererOptions {
  /** 是否启用去隔行（对元数据声明为隔行的视频）。 */
  deinterlace?: boolean;
  /** 是否启用画质增强（mosquito 降噪 + 上采样）。 */
  enhancement?: boolean;
  /** 降噪强度 0..1。 */
  noiseReduction?: number;
  /** 允许进入 WebGL 处理的源分辨率上限。 */
  maxSourceWidth?: number;
  maxSourceHeight?: number;
  /** 输出上限。 */
  maxOutputWidth?: number;
  maxOutputHeight?: number;
}

export interface RendererState {
  supported: boolean;
  active: boolean;
  deinterlacing: boolean;
  sourceWidth: number;
  sourceHeight: number;
}

const MAX_SRC_W = 1920;
const MAX_SRC_H = 1088;
const MAX_OUT_W = 3840;
const MAX_OUT_H = 2160;

/**
 * 顶点着色器：注意**纹理 V 要翻转**。
 * 视频帧用 texImage2D 直接上传且未设 UNPACK_FLIP_Y_WEBGL → v=0 对应图像**顶行**，
 * 因此必须把 V 取反，屏幕上方才对应画面正立的顶部；否则整幅画面上下颠倒
 * （曾出现"画面倒着"的线上问题）。
 */
const VERTEX_SHADER = `#version 300 es
in vec2 aPosition;
out vec2 vTexCoord;
void main() {
  vTexCoord = vec2(aPosition.x * 0.5 + 0.5, 0.5 - aPosition.y * 0.5);
  gl_Position = vec4(aPosition, 0.0, 1.0);
}`;

/**
 * 单通路：去隔行（场内插值混合）→ mosquito 降噪（边缘感知平滑）→ Catmull-Rom 上采样。
 */
const FRAGMENT_SHADER = `#version 300 es
precision mediump float;

uniform sampler2D uTexture;
uniform vec2 uSrcSize;
uniform vec2 uDstSize;
uniform float uDeinterlace;   // 0/1
uniform float uNR;            // 0..1 降噪强度
uniform float uUpscale;       // 0/1 是否做 Catmull-Rom 上采样

in vec2 vTexCoord;
out vec4 fragColor;

vec3 fetch(vec2 uv) {
  return texture(uTexture, clamp(uv, vec2(0.0), vec2(1.0))).rgb;
}

// Catmull-Rom 权重（一维）
void weights(float f, out float w0, out float w1, out float w2, out float w3) {
  float f2 = f * f;
  float f3 = f2 * f;
  w0 = -0.5 * f3 + f2 - 0.5 * f;
  w1 = 1.5 * f3 - 2.5 * f2 + 1.0;
  w2 = -1.5 * f3 + 2.0 * f2 + 0.5 * f;
  w3 = 0.5 * f3 - 0.5 * f2;
}

vec3 catmullRom(vec2 uv) {
  vec2 texel = 1.0 / uSrcSize;
  vec2 st = uv * uSrcSize - 0.5;
  vec2 iuv = floor(st);
  vec2 f = st - iuv;

  float wx0, wx1, wx2, wx3;
  float wy0, wy1, wy2, wy3;
  weights(f.x, wx0, wx1, wx2, wx3);
  weights(f.y, wy0, wy1, wy2, wy3);

  vec3 row0 = wx0 * fetch((iuv + vec2(-0.5, -0.5)) * texel)
            + wx1 * fetch((iuv + vec2( 0.5, -0.5)) * texel)
            + wx2 * fetch((iuv + vec2( 1.5, -0.5)) * texel)
            + wx3 * fetch((iuv + vec2( 2.5, -0.5)) * texel);
  vec3 row1 = wx0 * fetch((iuv + vec2(-0.5,  0.5)) * texel)
            + wx1 * fetch((iuv + vec2( 0.5,  0.5)) * texel)
            + wx2 * fetch((iuv + vec2( 1.5,  0.5)) * texel)
            + wx3 * fetch((iuv + vec2( 2.5,  0.5)) * texel);
  vec3 row2 = wx0 * fetch((iuv + vec2(-0.5,  1.5)) * texel)
            + wx1 * fetch((iuv + vec2( 0.5,  1.5)) * texel)
            + wx2 * fetch((iuv + vec2( 1.5,  1.5)) * texel)
            + wx3 * fetch((iuv + vec2( 2.5,  1.5)) * texel);
  vec3 row3 = wx0 * fetch((iuv + vec2(-0.5,  2.5)) * texel)
            + wx1 * fetch((iuv + vec2( 0.5,  2.5)) * texel)
            + wx2 * fetch((iuv + vec2( 1.5,  2.5)) * texel)
            + wx3 * fetch((iuv + vec2( 2.5,  2.5)) * texel);

  return wy0 * row0 + wy1 * row1 + wy2 * row2 + wy3 * row3;
}

void main() {
  vec2 uv = vTexCoord;
  vec2 texel = 1.0 / uSrcSize;

  vec3 col = (uUpscale > 0.5) ? catmullRom(uv) : fetch(uv);

  // 去隔行：当前行与上下行插值做场内混合
  if (uDeinterlace > 0.5) {
    vec3 up = fetch(vec2(uv.x, uv.y - texel.y));
    vec3 dn = fetch(vec2(uv.x, uv.y + texel.y));
    vec3 interp = (up + dn) * 0.5;
    col = mix(col, mix(col, interp, 0.5), 1.0);
  }

  // mosquito 降噪：边缘感知——平坦/弱纹理区域平滑更多，强边缘保留
  if (uNR > 0.0) {
    vec3 c = col;
    vec3 blur =
        fetch(uv + vec2(-texel.x, -texel.y)) + fetch(uv + vec2(0.0, -texel.y)) + fetch(uv + vec2(texel.x, -texel.y)) +
        fetch(uv + vec2(-texel.x, 0.0))      + c                                + fetch(uv + vec2(texel.x, 0.0)) +
        fetch(uv + vec2(-texel.x,  texel.y)) + fetch(uv + vec2(0.0,  texel.y)) + fetch(uv + vec2(texel.x,  texel.y));
    blur /= 9.0;
    float laplace = abs(dot(col - blur, vec3(0.299, 0.587, 0.114)));
    // 差异越小越像压缩振铃，平滑权重越高
    float w = uNR * (1.0 - smoothstep(0.0, 0.12, laplace));
    col = mix(col, blur, clamp(w, 0.0, 1.0));
  }

  fragColor = vec4(col, 1.0);
}`;

type RVFCVideo = HTMLVideoElement & {
  requestVideoFrameCallback?: (cb: (now: number) => void) => number;
  cancelVideoFrameCallback?: (handle: number) => void;
};

export class VideoRenderer {
  private gl: WebGL2RenderingContext | null = null;
  private program: WebGLProgram | null = null;
  private texture: WebGLTexture | null = null;
  private vao: WebGLVertexArrayObject | null = null;
  private rvfHandle = 0;
  private rafHandle = 0;
  private running = false;

  private readonly maxSrcW: number;
  private readonly maxSrcH: number;
  private readonly maxOutW: number;
  private readonly maxOutH: number;
  private deinterlace: boolean;
  private enhancement: boolean;
  private nr: number;

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly video: HTMLVideoElement,
    options: VideoRendererOptions = {},
  ) {
    this.deinterlace = options.deinterlace ?? false;
    this.enhancement = options.enhancement ?? false;
    this.nr = Math.max(0, Math.min(1, options.noiseReduction ?? 0.35));
    this.maxSrcW = options.maxSourceWidth ?? MAX_SRC_W;
    this.maxSrcH = options.maxSourceHeight ?? MAX_SRC_H;
    this.maxOutW = options.maxOutputWidth ?? MAX_OUT_W;
    this.maxOutH = options.maxOutputHeight ?? MAX_OUT_H;
  }

  /** 当前环境是否可用（WebGL2 且源分辨率在门限内）。 */
  get supported(): boolean {
    if (!this.ensureContext()) return false;
    const w = this.video.videoWidth;
    const h = this.video.videoHeight;
    if (w === 0 || h === 0) return true; // 元数据未就绪时先认为可用
    return w <= this.maxSrcW && h <= this.maxSrcH;
  }

  /**
   * WebGL 通路只在启用去隔行/画质增强时才接管呈现。
   * 否则原生 <video> 直接显示（少一次全屏上采样拷贝，也避免与画质增强无关的呈现路径）。
   */
  private get enabled(): boolean {
    return this.deinterlace || this.enhancement;
  }

  get state(): RendererState {
    const w = this.video.videoWidth;
    const h = this.video.videoHeight;
    return {
      supported: this.supported,
      active: this.running && this.enabled,
      deinterlacing: this.running && this.deinterlace,
      sourceWidth: w,
      sourceHeight: h,
    };
  }

  setDeinterlace(enabled: boolean): void {
    this.deinterlace = enabled;
  }

  setEnhancement(enabled: boolean): void {
    this.enhancement = enabled;
  }

  private ensureContext(): WebGL2RenderingContext | null {
    if (this.gl) return this.gl;
    const gl = this.canvas.getContext("webgl2", {
      alpha: false,
      antialias: false,
      depth: false,
      stencil: false,
      premultipliedAlpha: false,
    });
    if (!gl) return null;
    this.gl = gl;
    this.initPipeline(gl);
    return gl;
  }

  private initPipeline(gl: WebGL2RenderingContext): void {
    const program = buildProgram(gl, VERTEX_SHADER, FRAGMENT_SHADER);
    if (!program) return;
    this.program = program;

    const vao = gl.createVertexArray();
    const vbo = gl.createBuffer();
    gl.bindVertexArray(vao);
    gl.bindBuffer(gl.ARRAY_BUFFER, vbo);
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
    const loc = gl.getAttribLocation(program, "aPosition");
    gl.enableVertexAttribArray(loc);
    gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);
    this.vao = vao;

    const tex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, tex);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    this.texture = tex;
  }

  start(): void {
    if (this.running) return;
    if (!this.ensureContext()) return;
    this.running = true;
    this.scheduleNextFrame();
  }

  stop(): void {
    this.running = false;
    const v = this.video as RVFCVideo;
    if (this.rvfHandle && v.cancelVideoFrameCallback) v.cancelVideoFrameCallback(this.rvfHandle);
    if (this.rafHandle) cancelAnimationFrame(this.rafHandle);
    this.rvfHandle = 0;
    this.rafHandle = 0;
  }

  private scheduleNextFrame(): void {
    if (!this.running) return;
    const v = this.video as RVFCVideo;
    if (typeof v.requestVideoFrameCallback === "function") {
      this.rvfHandle = v.requestVideoFrameCallback(() => {
        this.renderFrame();
        this.scheduleNextFrame();
      });
    } else {
      this.rafHandle = requestAnimationFrame(() => {
        this.renderFrame();
        this.scheduleNextFrame();
      });
    }
  }

  private renderFrame(): void {
    const gl = this.gl;
    const program = this.program;
    if (!gl || !program || !this.texture) return;
    // 未启用去隔行/画质增强：交给原生 <video> 呈现，不做无谓的全屏绘制
    if (!this.enabled) return;
    const vw = this.video.videoWidth;
    const vh = this.video.videoHeight;
    if (vw === 0 || vh === 0) return;

    // 分辨率门限：超出范围直接不处理（由上层用原生 <video> 呈现）
    if (vw > this.maxSrcW || vh > this.maxSrcH) return;

    // 输出尺寸**按源比例等比**放大到显示区域（不足则保持 1:1）：
    // 旧实现按两轴各自取 max(source, display)，内在比例可能与源不一致，
    // 配上 CSS object-contain 会变成"先拉伸、再留边"。
    const displayW = this.canvas.clientWidth || vw;
    const displayH = this.canvas.clientHeight || vh;
    const maxScale = Math.min(this.maxOutW / vw, this.maxOutH / vh);
    const scale = Math.max(1, Math.min(displayW / vw, displayH / vh, maxScale));
    const outW = Math.round(vw * scale);
    const outH = Math.round(vh * scale);
    if (this.canvas.width !== outW || this.canvas.height !== outH) {
      this.canvas.width = outW;
      this.canvas.height = outH;
    }

    gl.bindTexture(gl.TEXTURE_2D, this.texture);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, this.video);

    gl.viewport(0, 0, outW, outH);
    gl.useProgram(program);
    gl.bindVertexArray(this.vao);

    gl.uniform1i(gl.getUniformLocation(program, "uTexture"), 0);
    gl.uniform2f(gl.getUniformLocation(program, "uSrcSize"), vw, vh);
    gl.uniform2f(gl.getUniformLocation(program, "uDstSize"), outW, outH);
    gl.uniform1f(gl.getUniformLocation(program, "uDeinterlace"), this.deinterlace ? 1 : 0);
    gl.uniform1f(gl.getUniformLocation(program, "uNR"), this.enhancement ? this.nr : 0);
    // 仅在放大时启用 Catmull-Rom，缩小/等比时用线性更快
    gl.uniform1f(gl.getUniformLocation(program, "uUpscale"), outW > vw && this.enhancement ? 1 : 0);

    gl.drawArrays(gl.TRIANGLES, 0, 3);
  }

  destroy(): void {
    this.stop();
    const gl = this.gl;
    if (!gl) return;
    if (this.texture) gl.deleteTexture(this.texture);
    if (this.vao) gl.deleteVertexArray(this.vao);
    if (this.program) gl.deleteProgram(this.program);
    this.gl = null;
    this.program = null;
    this.texture = null;
    this.vao = null;
  }
}

function buildProgram(
  gl: WebGL2RenderingContext,
  vsSource: string,
  fsSource: string,
): WebGLProgram | null {
  const vs = compileShader(gl, gl.VERTEX_SHADER, vsSource);
  const fs = compileShader(gl, gl.FRAGMENT_SHADER, fsSource);
  if (!vs || !fs) return null;
  const program = gl.createProgram();
  gl.attachShader(program, vs);
  gl.attachShader(program, fs);
  gl.linkProgram(program);
  gl.deleteShader(vs);
  gl.deleteShader(fs);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    gl.deleteProgram(program);
    return null;
  }
  return program;
}

function compileShader(
  gl: WebGL2RenderingContext,
  type: number,
  source: string,
): WebGLShader | null {
  const shader = gl.createShader(type);
  if (!shader) return null;
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    gl.deleteShader(shader);
    return null;
  }
  return shader;
}
