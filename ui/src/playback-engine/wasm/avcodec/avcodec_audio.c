/*
 * Unified software audio decoder (clean-room) for media-engine.
 *
 * Wraps FFmpeg libavcodec (LGPL-2.1+, no GPL components) into a single
 * standalone WASM that covers MP2 / MP3 / AC-3 / E-AC-3 / AAC:
 *   codec_id: 0=ac3, 1=eac3, 2=mp2, 3=mp3, 4=aac
 *
 * Strategy: keep a growable feed buffer (carry + new input). Elementary
 * frames are cut by the codec's av_parser (mpegaudio / ac3 / eac3 / aac
 * parser), which is robust across all codecs and needs no per-codec frame
 * tables. Each complete frame is sent via avcodec_send_packet; decoded
 * frames are written as interleaved float32 PCM. Frame formats handled:
 * FLTP / FLT / S16P / S16（定点 mp2/mp3 解码器输出 s16，转 float 交出）.
 * When channels > 2 the first two are kept (AC-3/E-AC-3 additionally
 * request internal stereo downmix via the "downmix" AVOption). Bytes the
 * parser has not consumed yet are kept as carry for the next call.
 *
 * Parser contract note: av_parser_parse2 may hand out a complete frame
 * from its internal buffer while returning consumed=0 (frame ends exactly
 * at the start of the new input). Such frames MUST be decoded — testing
 * with 768B-chunked AC-3 showed the old "break on consumed<=0 before
 * checking pkt_size" logic silently dropped every other frame.
 * Calling with input_len=0 runs a parser EOF flush (frees the frame held
 * back inside the parser at end of stream).
 *
 * Public ABI (exports, keep-alive):
 *   me_decoder_create(codec_id) -> ptr | 0
 *   me_decoder_destroy(ptr)
 *   me_decoder_reset(ptr)
 *   me_max_samples_per_frame()  // stereo estimate, for capacity planning
 *   me_decode_payload(ptr, in, inLen, outF32, outCapFloats, info[8]) -> totalSamples
 *
 * out_info[8] (i32): [samplesPerChannel, sampleRate, channels, frames,
 *   carryBytes, consumedBytes, samplesBeforeInput, errors]
 */

#include <libavcodec/avcodec.h>
#include <libavcodec/parser.h>
#include <libavutil/opt.h>

#ifdef __EMSCRIPTEN__
#include <emscripten.h>
#define EXPORT EMSCRIPTEN_KEEPALIVE
#else
#define EXPORT
#endif

#include <stdlib.h>
#include <string.h>

#define CARRY_MAX 8192
#define WORK_INIT 8192

typedef struct {
    AVCodecContext* ctx;
    AVCodecParserContext* parser;
    AVPacket* pkt;
    AVFrame* frame;
    unsigned char* work;
    int work_cap;
    int work_len;      /* bytes currently meaningful (leftover + appended) */
    int carry_size;    /* unparsed bytes retained at the front */
    int sample_rate;
    int error_count;
} MeDecoder;

static const enum AVCodecID CODEC_TABLE[5] = {
    AV_CODEC_ID_AC3, AV_CODEC_ID_EAC3, AV_CODEC_ID_MP2, AV_CODEC_ID_MP3, AV_CODEC_ID_AAC,
};

/* 前置声明（供 me_decoder_create 内错误路径调用） */
void me_decoder_destroy(MeDecoder* d);

EXPORT
MeDecoder* me_decoder_create(int codec_id) {
    if (codec_id < 0 || codec_id > 4) {
        return 0;
    }
    const AVCodec* codec = avcodec_find_decoder(CODEC_TABLE[codec_id]);
    if (!codec) {
        return 0;
    }
    MeDecoder* d = (MeDecoder*)calloc(1, sizeof(MeDecoder));
    if (!d) {
        return 0;
    }
    d->ctx = avcodec_alloc_context3(codec);
    if (!d->ctx) {
        free(d);
        return 0;
    }
    if (codec_id == 0 || codec_id == 1) {
        /* AC-3/E-AC-3: ask libavcodec to downmix to stereo internally */
        av_opt_set(d->ctx, "downmix", "stereo", AV_OPT_SEARCH_CHILDREN);
    }
    if (avcodec_open2(d->ctx, codec, 0) < 0) {
        avcodec_free_context(&d->ctx);
        free(d);
        return 0;
    }
    d->parser = av_parser_init(codec->id);
    d->pkt = av_packet_alloc();
    d->frame = av_frame_alloc();
    d->work_cap = WORK_INIT;
    d->work = (unsigned char*)malloc(d->work_cap);
    if (!d->parser || !d->pkt || !d->frame || !d->work) {
        me_decoder_destroy(d);
        return 0;
    }
    return d;
}

EXPORT
void me_decoder_destroy(MeDecoder* d) {
    if (!d) {
        return;
    }
    if (d->parser) {
        av_parser_close(d->parser);
    }
    av_packet_free(&d->pkt);
    av_frame_free(&d->frame);
    avcodec_free_context(&d->ctx);
    free(d->work);
    free(d);
}

EXPORT
void me_decoder_reset(MeDecoder* d) {
    if (!d) {
        return;
    }
    avcodec_flush_buffers(d->ctx);
    if (d->parser) {
        av_parser_close(d->parser);
        d->parser = av_parser_init(d->ctx->codec_id);
    }
    d->work_len = 0;
    d->carry_size = 0;
}

EXPORT
int me_max_samples_per_frame(void) {
    return 2048 * 2;
}

static int ensure_work(MeDecoder* d, int need) {
    if (need <= d->work_cap) {
        return 0;
    }
    int cap = d->work_cap;
    while (cap < need) {
        cap *= 2;
    }
    unsigned char* grown = (unsigned char*)realloc(d->work, cap);
    if (!grown) {
        return -1;
    }
    d->work = grown;
    d->work_cap = cap;
    return 0;
}

/* 把一个完整帧送入解码器并把输出 PCM 写入 out（立体声交织 float32）。
 * 返回本次新增样本数；*frames/*errors 累加。before_input 表示帧起始于本次
 * 输入之前（carry 区内或 parser 内部缓冲），其样本计入 *samples_before_input。 */
static int me_output_frame(
    MeDecoder* d,
    const unsigned char* pkt_buf,
    int pkt_size,
    float* out,
    int out_cap,
    int* total_samples,
    int* frames,
    int* errors,
    int before_input,
    int* samples_before_input
) {
    av_packet_unref(d->pkt);
    d->pkt->data = (unsigned char*)pkt_buf;
    d->pkt->size = pkt_size;
    if (avcodec_send_packet(d->ctx, d->pkt) < 0) {
        (*errors)++;
        return 0;
    }
    int added = 0;
    while (avcodec_receive_frame(d->ctx, d->frame) >= 0) {
        int nb = d->frame->nb_samples;
        int ch = d->frame->ch_layout.nb_channels;
        int out_ch = ch > 1 ? 2 : 1;
        if (nb <= 0 || ch < 1) {
            av_frame_unref(d->frame);
            continue;
        }
        if (*total_samples + nb * out_ch > out_cap) {
            av_frame_unref(d->frame);
            break; /* out of space: stop decoding this payload */
        }
        float* dst = out + (size_t)*total_samples * 2;
        if (d->frame->format == AV_SAMPLE_FMT_FLTP) {
            /* planar：取前两声道（ac3 已内部 downmix 为 2ch） */
            float* l = (float*)d->frame->extended_data[0];
            float* r = ch > 1 ? (float*)d->frame->extended_data[1] : l;
            for (int i = 0; i < nb; i++) {
                dst[i * 2] = l[i];
                dst[i * 2 + 1] = r[i];
            }
        } else if (d->frame->format == AV_SAMPLE_FMT_FLT) {
            /* packed：单缓冲按声道交错 */
            float* base = (float*)d->frame->extended_data[0];
            for (int i = 0; i < nb; i++) {
                dst[i * 2] = base[i * ch];
                dst[i * 2 + 1] = base[i * ch + (ch > 1 ? 1 : 0)];
            }
        } else if (d->frame->format == AV_SAMPLE_FMT_S16P) {
            /* 定点解码器（裁剪构建的 mp2/mp3 为 fixed 版）输出平面 s16，
             * 转 float 交出（/32768 归一），否则帧被静默丢弃 → 无声 */
            const int16_t* l = (const int16_t*)d->frame->extended_data[0];
            const int16_t* r = ch > 1 ? (const int16_t*)d->frame->extended_data[1] : l;
            for (int i = 0; i < nb; i++) {
                dst[i * 2] = l[i] * (1.0f / 32768.0f);
                dst[i * 2 + 1] = r[i] * (1.0f / 32768.0f);
            }
        } else if (d->frame->format == AV_SAMPLE_FMT_S16) {
            /* packed s16（Layer I 等） */
            const int16_t* base = (const int16_t*)d->frame->extended_data[0];
            for (int i = 0; i < nb; i++) {
                dst[i * 2] = base[i * ch] * (1.0f / 32768.0f);
                dst[i * 2 + 1] = base[i * ch + (ch > 1 ? 1 : 0)] * (1.0f / 32768.0f);
            }
        } else {
            av_frame_unref(d->frame);
            continue;
        }
        if (d->sample_rate == 0) {
            d->sample_rate = d->frame->sample_rate;
        }
        *total_samples += nb;
        (*frames)++;
        av_frame_unref(d->frame);
        added += nb;
    }
    if (before_input && samples_before_input) {
        *samples_before_input += added;
    }
    return added;
}

EXPORT
int me_decode_payload(
    MeDecoder* d,
    const unsigned char* input,
    int input_len,
    float* out,
    int out_cap,
    int* info
) {
    if (!d || !out || !info || input_len < 0) {
        return 0;
    }
    memset(info, 0, 8 * sizeof(int));

    /* 记录本次输入之前的 carry 字节数（随后会被 memmove 移动到 work 头部），
     * 用于判定本批首个完整帧是否起始于本次输入之前（跨 payload 拼帧）。 */
    int carry_at_start = d->carry_size;

    if (ensure_work(d, d->carry_size + input_len) < 0) {
        return 0;
    }
    /* compact leftover to the front, then append the new input */
    if (d->carry_size > 0 && d->carry_size != d->work_len) {
        memmove(d->work, d->work + (d->work_len - d->carry_size), d->carry_size);
    }
    memcpy(d->work + d->carry_size, input, input_len);
    d->work_len = d->carry_size + input_len;

    int total = d->work_len;
    int pos = 0;
    int total_samples = 0;
    int frames = 0;
    int errors = 0;
    int samples_before_input = 0;

    while (pos < total) {
        const unsigned char* pkt_buf = 0;
        int pkt_size = 0;
        uintptr_t w_start = (uintptr_t)d->work;
        uintptr_t w_end = w_start + (uintptr_t)d->work_len;
        int consumed = av_parser_parse2(
            d->parser, d->ctx, &pkt_buf, &pkt_size,
            d->work + pos, total - pos,
            AV_NOPTS_VALUE, AV_NOPTS_VALUE, 0);
        if (consumed < 0) {
            consumed = 0; /* av_parser_parse2 已夹 0，此处防御 */
        }
        /* 关键：parser 可能交出其内部缓冲的完整帧同时返回 consumed=0
         * （帧恰好结束于本次输入起点）。此时帧有效，必须送解码器，
         * 且本次输入未消耗任何字节（帧来自 parser 内部缓冲）。
         * 旧行为在检查 pkt_size 之前就因 consumed<=0 break，导致丢帧
         * （AC-3 768B 分块喂入实测丢一半）。 */
        if (pkt_size > 0 && pkt_buf) {
            uintptr_t f_start = (uintptr_t)pkt_buf;
            /* 帧起始于 carry 区内（跨 payload 拼帧）或 parser 内部缓冲
             * （数据来自先前输入）→ 属于本次输入之前 */
            int before_input = f_start >= w_start && f_start < w_end
                ? (int)(f_start - w_start) < carry_at_start
                : 1;
            me_output_frame(
                d, pkt_buf, pkt_size, out, out_cap,
                &total_samples, &frames, &errors, before_input, &samples_before_input);
        }
        if (consumed <= 0) {
            break; /* 本次输入无进展：留作 carry，等更多数据 */
        }
        pos += consumed;
    }

    if (input_len == 0) {
        /* EOF flush（flush() 路径）：buf_size=0 触发 parser 交出内部滞留帧。
         * 注意即使 carry=0 也要调——末帧可能已被 parser 收进其内部缓冲
         * （对我们报告为已消耗），pc 里滞留的帧只有 EOF flush 能取出。 */
        const unsigned char* pkt_buf = 0;
        int pkt_size = 0;
        av_parser_parse2(
            d->parser, d->ctx, &pkt_buf, &pkt_size,
            d->work + total, 0,
            AV_NOPTS_VALUE, AV_NOPTS_VALUE, 0);
        if (pkt_size > 0 && pkt_buf) {
            me_output_frame(
                d, pkt_buf, pkt_size, out, out_cap,
                &total_samples, &frames, &errors, 1, &samples_before_input);
        }
        pos = total; /* flush 后丢弃全部残余 */
    }

    int remaining = total - pos;
    if (remaining > CARRY_MAX) {
        pos += remaining - CARRY_MAX;
        remaining = CARRY_MAX;
    }
    if (remaining > 0) {
        memmove(d->work, d->work + pos, remaining);
    }
    d->carry_size = remaining;
    d->work_len = remaining;

    info[0] = total_samples;
    info[1] = d->sample_rate;
    info[2] = 2; /* 立体声交织输出（<=2 声道处理见上） */
    info[3] = frames;
    info[4] = remaining;
    info[5] = pos;
    info[6] = samples_before_input;
    info[7] = errors;
    return total_samples;
}

/* emscripten 独立模式的 libc 裁剪未携带 musl 时区内部符号；
 * 解码路径不触发（FFmpeg 时间/日志路径用），打桩为 UTC 偏移即可。 */
void __secs_to_zone(long long t, int isdst, int* dst, long* off0, long* off1, const char** names) {
    if (dst) {
        *dst = isdst;
    }
    if (off0) {
        *off0 = 0;
    }
    if (off1) {
        *off1 = 0;
    }
    if (names) {
        *names = "";
    }
    (void)t;
}
