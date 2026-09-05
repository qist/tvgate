/*
 * AC-3 / E-AC-3 (Dolby Digital / DD+) Decoder - WebAssembly Wrapper
 *
 * 基于 FFmpeg libavcodec 裁剪构建（仅 ac3/eac3 解码器，LGPL-2.1+）。
 * 解码输出固定为 2.0 立体声 float32 交织 PCM——通过 ffmpeg 解码器的
 * request_channel_layout 在解码器内部完成 5.1→stereo downmix（含 dialnorm）。
 *
 * 与 mp2_decoder.c 相同的 payload 语义：输入是 PES payload，内部循环处理
 * 所有完整帧，尾部不完整帧保留在 carry buffer 与下次输入拼接。
 * AC-3 按 syncword 0x0B77 + 帧长表切帧；E-AC-3 按 0x0B77 + frmsiz 字段切帧。
 */

#include <libavcodec/avcodec.h>
#include <libavutil/opt.h>

#ifdef __EMSCRIPTEN__
#include <emscripten.h>
#define EXPORT EMSCRIPTEN_KEEPALIVE
#else
#define EXPORT
#endif

#include <stdlib.h>
#include <string.h>

#define CARRY_MAX 4096

/* AC-3 帧长表（字节）：[sampling_rate_code][frame_size_code]，
 * 与 ATSC A/52 frmsizecod 一致。 */
static const int ac3_frame_sizes[3][38] = {
    {64, 64, 80, 80, 96, 96, 112, 112, 128, 128, 160, 160, 192, 192, 224, 224,
     256, 256, 320, 320, 384, 384, 448, 448, 512, 512, 640, 640, 768, 768,
     896, 896, 1024, 1024, 1152, 1152, 1280, 1280},
    {69, 70, 87, 88, 104, 105, 121, 122, 139, 140, 174, 175, 208, 209, 243,
     244, 278, 279, 348, 349, 417, 418, 487, 488, 557, 558, 696, 697, 835,
     836, 975, 976, 1114, 1115, 1253, 1254, 1393, 1394},
    {96, 96, 120, 120, 144, 144, 168, 168, 192, 192, 240, 240, 288, 288, 336,
     336, 384, 384, 480, 480, 576, 576, 672, 672, 768, 768, 960, 960, 1152,
     1152, 1344, 1344, 1536, 1536, 1728, 1728, 1920, 1920},
};

typedef struct {
    AVCodecContext* ctx;
    AVPacket* pkt;
    AVFrame* frame;
    int is_eac3;
    int sample_rate;
    unsigned char carry[CARRY_MAX];
    int carry_size;
    int error_count;
} Ac3Decoder;

/* 从 AC-3 帧头解析帧长；非 sync 位置返回 0 */
static int ac3_frame_length(const unsigned char* p, int is_eac3) {
    if (p[0] != 0x0b || p[1] != 0x77) {
        return 0;
    }
    if (!is_eac3) {
        int fscod = (p[4] >> 6) & 0x03;
        int frmsizecod = p[4] & 0x3f;
        if (fscod == 3 || frmsizecod >= 38) {
            return 0;
        }
        return ac3_frame_sizes[fscod][frmsizecod] * 2;
    }
    /* E-AC-3: frmsiz = 14 bits ((p[2]&0x03)<<12 | p[3]<<4 | p[4]>>4)，帧长 = (frmsiz+1)*2 */
    int frmsiz = ((p[2] & 0x03) << 12) | (p[3] << 4) | (p[4] >> 4);
    return (frmsiz + 1) * 2;
}

EXPORT
Ac3Decoder* ac3_decoder_create(int is_eac3) {
    enum AVCodecID codec_id = is_eac3 ? AV_CODEC_ID_EAC3 : AV_CODEC_ID_AC3;
    const AVCodec* codec = avcodec_find_decoder(codec_id);
    if (!codec) {
        return NULL;
    }
    Ac3Decoder* d = (Ac3Decoder*)calloc(1, sizeof(Ac3Decoder));
    if (!d) {
        return NULL;
    }
    d->ctx = avcodec_alloc_context3(codec);
    if (!d->ctx) {
        free(d);
        return NULL;
    }
    /* 请求解码器内部 downmix 为立体声（含 dialnorm/center mix 系数）。
     * FFmpeg 7.x：ac3/eac3 浮点解码器通过 "downmix" AVOption 请求。 */
    av_opt_set(d->ctx, "downmix", "stereo", AV_OPT_SEARCH_CHILDREN);
    if (avcodec_open2(d->ctx, codec, NULL) < 0) {
        avcodec_free_context(&d->ctx);
        free(d);
        return NULL;
    }
    d->pkt = av_packet_alloc();
    d->frame = av_frame_alloc();
    d->is_eac3 = is_eac3 ? 1 : 0;
    return d;
}

EXPORT
void ac3_decoder_destroy(Ac3Decoder* d) {
    if (!d) {
        return;
    }
    av_packet_free(&d->pkt);
    av_frame_free(&d->frame);
    avcodec_free_context(&d->ctx);
    free(d);
}

EXPORT
void ac3_decoder_reset(Ac3Decoder* d) {
    if (!d) {
        return;
    }
    avcodec_flush_buffers(d->ctx);
    d->carry_size = 0;
}

EXPORT
int ac3_max_samples_per_frame(void) {
    return 1536 * 2; /* 每帧最大样本数（stereo 交织） */
}

/*
 * 解码 (carry + input) 中的所有完整帧。
 *
 * out_info 布局（8 × i32）：
 *   [0] 总输出样本数/声道（stereo）
 *   [1] 采样率
 *   [2] 声道数（固定 2）
 *   [3] 解码帧数
 *   [4] carry 残余字节数
 *   [5] 消耗的输入字节数（含上次 carry）
 *   [6] 来自此输入之前帧的样本数（跨 payload 拼帧）
 *   [7] 解码错误帧数
 * 返回：总输出样本数/声道（0 = 无完整帧或全部失败）
 */
EXPORT
int ac3_decode_payload(
    Ac3Decoder* d,
    const unsigned char* input,
    int input_size,
    float* output,
    int output_capacity,
    int* out_info
) {
    if (!d || !input || input_size < 0 || !out_info) {
        return 0;
    }
    memset(out_info, 0, 8 * sizeof(int));

    int carry_at_start = d->carry_size;
    int total_in = carry_at_start + input_size;
    if (total_in == 0) {
        return 0;
    }
    unsigned char* work = (unsigned char*)malloc(total_in);
    if (!work) {
        return 0;
    }
    if (carry_at_start > 0) {
        memcpy(work, d->carry, carry_at_start);
    }
    memcpy(work + carry_at_start, input, input_size);

    int offset = 0;
    int total_samples = 0;
    int frames = 0;
    int errors = 0;
    int samples_before_input = 0;

    while (total_in - offset >= 6) {
        const unsigned char* hdr = work + offset;
        if (hdr[0] != 0x0b || hdr[1] != 0x77) {
            offset++;
            continue;
        }
        int frame_len = ac3_frame_length(hdr, d->is_eac3);
        if (frame_len < 6) {
            /* 表内非法组合（损坏头部）：跳过 syncword 继续 */
            offset += 2;
            continue;
        }
        if (offset + frame_len > total_in) {
            /* 尾部不完整帧：保留 carry */
            break;
        }

        av_packet_unref(d->pkt);
        d->pkt->data = (unsigned char*)hdr;
        d->pkt->size = frame_len;
        int ret = avcodec_send_packet(d->ctx, d->pkt);
        if (ret < 0) {
            errors++;
            offset += frame_len;
            continue;
        }
        while (avcodec_receive_frame(d->ctx, d->frame) >= 0) {
            int nb = d->frame->nb_samples;
            int ch = d->frame->ch_layout.nb_channels;
            if (ch < 1) {
                av_frame_unref(d->frame);
                continue;
            }
            if ((total_samples + nb) * 2 > output_capacity) {
                /* 输出缓冲不足：保留本帧与后续数据到 carry（通过回退 offset 实现） */
                av_frame_unref(d->frame);
                break;
            }
            if (d->frame->format == AV_SAMPLE_FMT_FLTP) {
                /* planar float → 交织 stereo（解码器已 downmix，ch 通常为 2） */
                float* l = (float*)d->frame->extended_data[0];
                float* r = ch > 1 ? (float*)d->frame->extended_data[1] : l;
                float* outp = output + (size_t)total_samples * 2;
                for (int i = 0; i < nb; i++) {
                    outp[i * 2] = l[i];
                    outp[i * 2 + 1] = r[i];
                }
            } else if (d->frame->format == AV_SAMPLE_FMT_FLT) {
                float* src = (float*)d->frame->extended_data[0];
                float* outp = output + (size_t)total_samples * 2;
                for (int i = 0; i < nb; i++) {
                    outp[i * 2] = src[i * ch];
                    outp[i * 2 + 1] = src[i * ch + (ch > 1 ? 1 : 0)];
                }
            } else {
                av_frame_unref(d->frame);
                continue;
            }
            if (d->sample_rate != d->frame->sample_rate) {
                if (total_samples > 0) {
                    /* 采样率变化：本 payload 到此为止，剩余进 carry 下次解 */
                    av_frame_unref(d->frame);
                    break;
                }
                d->sample_rate = d->frame->sample_rate;
            }
            if (offset < carry_at_start) {
                samples_before_input += nb;
            }
            total_samples += nb;
            frames++;
            av_frame_unref(d->frame);
        }
        offset += frame_len;
    }

    int remaining = total_in - offset;
    if (remaining > CARRY_MAX) {
        offset += remaining - CARRY_MAX;
        remaining = CARRY_MAX;
    }
    if (remaining > 0) {
        memmove(d->carry, work + offset, remaining);
    }
    d->carry_size = remaining;

    free(work);
    out_info[0] = total_samples;
    out_info[1] = d->sample_rate;
    out_info[2] = 2;
    out_info[3] = frames;
    out_info[4] = remaining;
    out_info[5] = offset;
    out_info[6] = samples_before_input;
    out_info[7] = errors;
    return total_samples;
}
