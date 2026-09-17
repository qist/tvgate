/*
 * WSOLA（Waveform Similarity Overlap-Add）时域拉伸 —— 本项目自研实现。
 *
 * 算法（公开算法，非移植）：保音高的变速播放
 *   1) 以固定「合成步长 seq」连续输出帧；
 *   2) 每次合成时，在理想分析位置附近 ±seek 范围内搜索与上一段输出尾部
 *      （tail）波形最相似的候选段——评分用归一化互相关，避免偏向高能量位置；
 *   3) 用升余弦窗在 overlap 帧上做交叉淡化，掩盖拼接处的相位跳变。
 *   输出时长 / 输入时长 = 1 / ratio。
 *
 * 与 JS 封装（audio/wasm-stretcher.ts）的接口契约：
 *   - wsola_create(sampleRate, channels)：channels ∈ [1, WSOLA_MAX_CHANNELS]，失败返回 NULL；
 *   - wsola_set_ratio(ratio)：夹到 [0.5, 2.0]；|ratio - 1| < 1e-3 时走直通（零失真）；
 *   - wsola_position()：返回「已产出输出对应的输入位置」（绝对帧号，double），
 *     调用方用它把输出映射回流时间；ratio 变化不重置该值；
 *   - wsola_process()：喂入 in_frames 帧交错 PCM，写出至多 out_capacity 帧，
 *     返回实际写出帧数（0 表示输入还凑不满一个合成周期）；
 *   - wsola_reset()：清空输入与位置，但保留 ratio（重同步后仍按当前变速播放）。
 *
 * 实现要点（与其他实现的主要差异）：
 *   - 输入用**环形缓冲**保存，丢弃已消费前缀是 O(1)（无需 memmove 整块数据）；
 *   - 搜索窗的单声道混合 + 前缀能量表：候选段能量 O(1) 取得，评分只看波形相似度；
 *   - 单声道混合对**所有声道**求和（多声道内容的相似度判定更稳）。
 */

#ifdef __EMSCRIPTEN__
#include <emscripten.h>
#define WSOLA_API EMSCRIPTEN_KEEPALIVE
#else
#define WSOLA_API
#endif

#include <math.h>
#include <stdlib.h>
#include <string.h>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

/* 合成参数：30ms 合成周期 / 12ms 交叉淡化 / ±10ms 搜索半径
 * （WSOLA 的经典取值区间，48kHz 下约 1440 / 576 / 480 帧）。 */
#define WSOLA_SEQ_MS 30
#define WSOLA_OVERLAP_MS 12
#define WSOLA_SEEK_MS 10

/* 软解 PCM 直通 5.1：拉伸器须支持多声道（3 声道以上时若上限不足，
 * wsola_create 会失败 → 软解音频整体不可用）。 */
#define WSOLA_MAX_CHANNELS 6

#define WSOLA_RATIO_MIN 0.5f
#define WSOLA_RATIO_MAX 2.0f
/* 视为 1.0 的容差：此范围内不做合成，直接按输入原样输出（零失真）。 */
#define WSOLA_BYPASS_EPSILON 0.001f

typedef struct {
    int sample_rate;
    int channels;

    float ratio; /* 目标速度（>1 加快、<1 放慢） */
    int seq;     /* 每个合成周期输出的帧数 */
    int overlap; /* 交叉淡化长度（帧） */
    int seek;    /* 相似度搜索半径（帧） */

    float* ring;      /* 环形输入缓冲（交错存储，单位：帧） */
    int ring_cap;     /* 容量（帧） */
    int ring_head;    /* 最早一帧在 ring 中的下标（帧） */
    int ring_len;     /* 当前帧数 */
    double ring_base; /* ring 中最早一帧的绝对帧号 */

    double cursor; /* 已产出输出对应的绝对输入位置（wsola_position 的返回值） */

    int has_tail; /* tail 是否已建立 */
    float* tail;  /* overlap * channels：上一段输出的未加窗延续参考 */

    float* window; /* overlap 个升余弦系数（交叉淡化用） */
    float* ref;    /* overlap 个：tail 的单声道混合（相似度评分的参考波形） */

    float* mono;   /* 搜索窗单声道混合：2*seek + overlap 帧 */
    float* energy; /* mono 的前缀能量：2*seek + overlap + 1 个 */
    int scratch_cap;
} WsolaState;

/* ---------------- 环形缓冲 ---------------- */

static int wsola_ring_reserve(WsolaState* w, int frames) {
    if (frames <= w->ring_cap) {
        return 1;
    }
    int cap = w->ring_cap > 0 ? w->ring_cap : 4096;
    while (cap < frames) {
        cap *= 2;
    }
    float* grown = (float*)malloc(sizeof(float) * (size_t)cap * (size_t)w->channels);
    if (!grown) {
        return 0;
    }
    if (w->ring_len > 0) {
        /* 拉直：把环形数据按"最早帧在头部"的顺序拷进新缓冲 */
        int head_to_end = w->ring_cap - w->ring_head;
        if (head_to_end > w->ring_len) {
            head_to_end = w->ring_len;
        }
        memcpy(grown, w->ring + (size_t)w->ring_head * w->channels,
               sizeof(float) * (size_t)head_to_end * (size_t)w->channels);
        if (w->ring_len > head_to_end) {
            memcpy(grown + (size_t)head_to_end * w->channels, w->ring,
                   sizeof(float) * (size_t)(w->ring_len - head_to_end) * (size_t)w->channels);
        }
    }
    free(w->ring);
    w->ring = grown;
    w->ring_cap = cap;
    w->ring_head = 0;
    return 1;
}

static void wsola_ring_push(WsolaState* w, const float* src, int frames) {
    const size_t ch = (size_t)w->channels;
    int write_at = w->ring_head + w->ring_len;
    if (write_at >= w->ring_cap) {
        write_at -= w->ring_cap;
    }
    int until_end = w->ring_cap - write_at;
    if (until_end > frames) {
        until_end = frames;
    }
    memcpy(w->ring + (size_t)write_at * ch, src, sizeof(float) * (size_t)until_end * ch);
    if (frames > until_end) {
        memcpy(w->ring, src + (size_t)until_end * ch, sizeof(float) * (size_t)(frames - until_end) * ch);
    }
    w->ring_len += frames;
}

static void wsola_ring_drop(WsolaState* w, int frames) {
    if (frames <= 0) {
        return;
    }
    if (frames > w->ring_len) {
        frames = w->ring_len;
    }
    w->ring_head += frames;
    if (w->ring_head >= w->ring_cap) {
        w->ring_head -= w->ring_cap;
    }
    w->ring_len -= frames;
    w->ring_base += (double)frames;
}

/* 把绝对位置 abs_pos 起的 frames 帧拷到 dst（调用方保证该区间在缓冲内）。 */
static void wsola_ring_read(const WsolaState* w, double abs_pos, float* dst, int frames) {
    const size_t ch = (size_t)w->channels;
    int rel = (int)llround(abs_pos - w->ring_base);
    int idx = w->ring_head + rel;
    if (idx >= w->ring_cap) {
        idx -= w->ring_cap;
    }
    int until_end = w->ring_cap - idx;
    if (until_end > frames) {
        until_end = frames;
    }
    memcpy(dst, w->ring + (size_t)idx * ch, sizeof(float) * (size_t)until_end * ch);
    if (frames > until_end) {
        memcpy(dst + (size_t)until_end * ch, w->ring, sizeof(float) * (size_t)(frames - until_end) * ch);
    }
}

/* 搜索窗的单声道混合（跨环绕点逐帧取样，避免再拷一份交错数据）。 */
static void wsola_mono_mix(const WsolaState* w, double abs_pos, int frames, float* dst) {
    const int ch = w->channels;
    int idx = w->ring_head + (int)llround(abs_pos - w->ring_base);
    if (idx >= w->ring_cap) {
        idx -= w->ring_cap;
    }
    for (int i = 0; i < frames; i++) {
        const float* frame = w->ring + (size_t)idx * (size_t)ch;
        float acc = 0.0f;
        for (int c = 0; c < ch; c++) {
            acc += frame[c];
        }
        dst[i] = acc;
        if (++idx >= w->ring_cap) {
            idx = 0;
        }
    }
}

static int wsola_reserve_scratch(WsolaState* w, int span) {
    if (span <= w->scratch_cap) {
        return 1;
    }
    float* mono = (float*)realloc(w->mono, sizeof(float) * (size_t)span);
    if (!mono) {
        return 0;
    }
    w->mono = mono;
    float* energy = (float*)realloc(w->energy, sizeof(float) * (size_t)(span + 1));
    if (!energy) {
        return 0;
    }
    w->energy = energy;
    w->scratch_cap = span;
    return 1;
}

/* ---------------- 对外接口 ---------------- */

WSOLA_API
WsolaState* wsola_create(int sample_rate, int channels) {
    if (sample_rate <= 0 || channels <= 0 || channels > WSOLA_MAX_CHANNELS) {
        return NULL;
    }

    WsolaState* w = (WsolaState*)calloc(1, sizeof(WsolaState));
    if (!w) {
        return NULL;
    }
    w->sample_rate = sample_rate;
    w->channels = channels;
    w->ratio = 1.0f;
    /* 至少 1 帧，避免极低采样率下出现零长度周期（正常采样率下与按毫秒换算一致）。 */
    w->seq = sample_rate * WSOLA_SEQ_MS / 1000;
    w->overlap = sample_rate * WSOLA_OVERLAP_MS / 1000;
    w->seek = sample_rate * WSOLA_SEEK_MS / 1000;
    if (w->seq < 1) w->seq = 1;
    if (w->overlap < 1) w->overlap = 1;
    if (w->seek < 1) w->seek = 1;

    const size_t overlap_ch = (size_t)w->overlap * (size_t)channels;
    w->tail = (float*)calloc(overlap_ch, sizeof(float));
    w->window = (float*)malloc(sizeof(float) * (size_t)w->overlap);
    w->ref = (float*)malloc(sizeof(float) * (size_t)w->overlap);
    if (!w->tail || !w->window || !w->ref) {
        free(w->tail);
        free(w->window);
        free(w->ref);
        free(w);
        return NULL;
    }
    /* 升余弦：半开区间取样（i + 0.5），避免首尾系数取到 0/1 的极端值。 */
    for (int i = 0; i < w->overlap; i++) {
        w->window[i] = 0.5f - 0.5f * cosf((float)M_PI * ((float)i + 0.5f) / (float)w->overlap);
    }
    return w;
}

WSOLA_API
void wsola_destroy(WsolaState* w) {
    if (!w) {
        return;
    }
    free(w->ring);
    free(w->tail);
    free(w->window);
    free(w->ref);
    free(w->mono);
    free(w->energy);
    free(w);
}

WSOLA_API
void wsola_reset(WsolaState* w) {
    if (!w) {
        return;
    }
    w->ring_head = 0;
    w->ring_len = 0;
    w->ring_base = 0.0;
    w->cursor = 0.0;
    w->has_tail = 0;
    /* 刻意保留 ratio：重同步后仍按当前变速播放。 */
}

WSOLA_API
void wsola_set_ratio(WsolaState* w, float ratio) {
    if (!w) {
        return;
    }
    if (ratio < WSOLA_RATIO_MIN) {
        ratio = WSOLA_RATIO_MIN;
    } else if (ratio > WSOLA_RATIO_MAX) {
        ratio = WSOLA_RATIO_MAX;
    }
    w->ratio = ratio;
}

WSOLA_API
double wsola_position(WsolaState* w) {
    return w ? w->cursor : 0.0;
}

/* 直通：ratio ≈ 1，输入原样输出（位精确）。 */
static int wsola_drain_passthrough(WsolaState* w, float* output, int out_capacity) {
    const size_t ch = (size_t)w->channels;
    int rel = (int)floor(w->cursor - w->ring_base);
    if (rel < 0) {
        /* 位置落在缓冲之前（不应发生）：前移到缓冲起点。 */
        w->cursor = w->ring_base;
        rel = 0;
    }
    int available = w->ring_len - rel;
    if (available <= 0) {
        return 0;
    }
    int frames = available < out_capacity ? available : out_capacity;
    if (frames <= 0) {
        return 0;
    }

    wsola_ring_read(w, w->ring_base + (double)rel, output, frames);

    if (w->has_tail) {
        /* 从拉伸切回直通：与残留的 tail 交叉淡化，避免拼接咔哒。
         * 淡化用窗函数的后半段（渐入到 1），使过渡从 tail 平滑接管到直通数据。 */
        int fade = w->overlap < frames ? w->overlap : frames;
        for (int i = 0; i < fade; i++) {
            const float a = w->window[w->overlap - fade + i];
            const float* tail_frame = w->tail + (size_t)i * ch;
            float* out_frame = output + (size_t)i * ch;
            for (size_t c = 0; c < ch; c++) {
                out_frame[c] = tail_frame[c] * (1.0f - a) + out_frame[c] * a;
            }
        }
        w->has_tail = 0;
    }

    w->cursor += (double)frames;
    return frames;
}

/* 拉伸：每个合成周期搜索最佳拼接点并交叉淡化。 */
static int wsola_synthesize(WsolaState* w, float* output, int out_capacity) {
    const size_t ch = (size_t)w->channels;
    /* 一个合成周期在理想位置之后需要的帧数：搜索半径 + 输出长度 + 一次淡化。 */
    const int cycle_span = w->seek + w->seq + w->overlap;

    if (!w->has_tail) {
        /* 首次进入拉伸：以当前位置之前的一小段作为延续参考（不足则补零）。 */
        memset(w->tail, 0, sizeof(float) * (size_t)w->overlap * ch);
        int rel = (int)floor(w->cursor - w->ring_base) - w->overlap;
        if (rel >= 0 && rel + w->overlap <= w->ring_len) {
            wsola_ring_read(w, w->ring_base + (double)rel, w->tail, w->overlap);
        }
        w->has_tail = 1;
    }

    int produced = 0;
    for (;;) {
        if (produced + w->seq > out_capacity) {
            break; /* 输出空间不足 */
        }
        if (w->cursor + (double)cycle_span > w->ring_base + (double)w->ring_len) {
            break; /* 输入不足一个完整合成周期 */
        }

        int center = (int)llround(w->cursor - w->ring_base);
        int low = center - w->seek;
        if (low < 0) {
            low = 0;
        }
        const int high = center + w->seek;
        const int span = high - low + w->overlap;
        if (!wsola_reserve_scratch(w, span)) {
            break;
        }

        /* 搜索窗：单声道混合 + 前缀能量（候选段能量 O(1) 取得）。 */
        wsola_mono_mix(w, w->ring_base + (double)low, span, w->mono);
        w->energy[0] = 0.0f;
        for (int i = 0; i < span; i++) {
            w->energy[i + 1] = w->energy[i] + w->mono[i] * w->mono[i];
        }
        /* 参考波形：tail 的单声道混合（相关性的极值位置与声道数无关）。 */
        for (int i = 0; i < w->overlap; i++) {
            const float* tail_frame = w->tail + (size_t)i * ch;
            float acc = 0.0f;
            for (size_t c = 0; c < ch; c++) {
                acc += tail_frame[c];
            }
            w->ref[i] = acc;
        }

        int best = center;
        float best_score = -1e30f;
        for (int q = low; q <= high; q++) {
            const float* candidate = w->mono + (q - low);
            float dot = 0.0f;
            for (int i = 0; i < w->overlap; i++) {
                dot += w->ref[i] * candidate[i];
            }
            const float candidate_energy = w->energy[q - low + w->overlap] - w->energy[q - low];
            const float score = dot / sqrtf(candidate_energy + 1e-9f);
            if (score > best_score) {
                best_score = score;
                best = q;
            }
        }

        /* 交叉淡化 overlap 帧（tail → 候选段），其后原样拷贝到 seq 边界。 */
        int idx = w->ring_head + best;
        if (idx >= w->ring_cap) {
            idx -= w->ring_cap;
        }
        for (int i = 0; i < w->overlap; i++) {
            const float a = w->window[i];
            const float* seg_frame = w->ring + (size_t)idx * ch;
            const float* tail_frame = w->tail + (size_t)i * ch;
            float* out_frame = output + (size_t)(produced + i) * ch;
            for (size_t c = 0; c < ch; c++) {
                out_frame[c] = tail_frame[c] * (1.0f - a) + seg_frame[c] * a;
            }
            if (++idx >= w->ring_cap) {
                idx = 0;
            }
        }
        wsola_ring_read(w, w->ring_base + (double)best + (double)w->overlap,
                        output + (size_t)(produced + w->overlap) * ch, w->seq - w->overlap);
        /* 新的延续参考 = 本段结尾的 overlap 帧（未加窗，供下次拼接比较）。 */
        wsola_ring_read(w, w->ring_base + (double)best + (double)w->seq, w->tail, w->overlap);

        produced += w->seq;
        /* 输出 seq 帧 ⇒ 分析位置前进 seq * ratio（ratio<1 拉伸时前进更慢）。 */
        w->cursor += (double)w->seq * (double)w->ratio;
    }

    return produced;
}

WSOLA_API
int wsola_process(WsolaState* w, const float* input, int in_frames, float* output, int out_capacity) {
    if (!w || in_frames < 0 || out_capacity < 0) {
        return 0;
    }
    if (out_capacity > 0 && !output) {
        return 0;
    }

    if (in_frames > 0) {
        if (!input) {
            return 0;
        }
        if (!wsola_ring_reserve(w, w->ring_len + in_frames)) {
            return 0;
        }
        wsola_ring_push(w, input, in_frames);
    }

    int produced;
    if (fabsf(w->ratio - 1.0f) < WSOLA_BYPASS_EPSILON) {
        produced = wsola_drain_passthrough(w, output, out_capacity);
    } else {
        produced = wsola_synthesize(w, output, out_capacity);
    }

    /* 丢弃不会再被引用的前缀（搜索半径 + 一次交叉淡化之外的数据）。 */
    int drop = (int)floor(w->cursor) - (w->seek + w->overlap) - (int)floor(w->ring_base);
    if (drop > 0) {
        wsola_ring_drop(w, drop);
    }

    return produced;
}
