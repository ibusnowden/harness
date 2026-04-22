// Copyright 2024 mmllm contributors
// Self-attention kernel implementation

#include <cuda_runtime.h>
#include <cmath>
#include <cstdio>
#include <cstdlib>

#define MLLM_CUDA_LAUNCH_CHECK(call) \
    do { \
        cudaError_t err = (call); \
        if (err != cudaSuccess) { \
            fprintf(stderr, "CUDA error: %s\n", cudaGetErrorString(err)); \
            exit(1); \
        } \
    } while(0)

// Rotary Position Embedding (RoPE)
__device__ void apply_rope(float* q, float* k, int head_dim, int pos,
                           const float* freqs) {
    for (int i = 0; i < head_dim / 2; i++) {
        float freq = freqs[i * 2];
        float theta = powf(10000.0f, -freq / head_dim);
        float inv_freq = 1.0f / (1.0f + logf(pos * theta) * (2.0f / head_dim));
        float angle = pos * theta * inv_freq;
        float cos_val = cosf(angle);
        float sin_val = sinf(angle);
        
        float q0 = q[i];
        float q1 = q[i + head_dim / 2];
        q[i] = q0 * cos_val - q1 * sin_val;
        q[i + head_dim / 2] = q0 * sin_val + q1 * cos_val;
        
        float k0 = k[i];
        float k1 = k[i + head_dim / 2];
        k[i] = k0 * cos_val - k1 * sin_val;
        k[i + head_dim / 2] = k0 * sin_val + k1 * cos_val;
    }
}

template <typename T>
__global__ void attention_forward_kernel(float* output, const float* q, 
                                          const float* k, const float* v,
                                          const float* freqs,
                                          int batch_size, int seq_len,
                                          int num_heads, int head_dim,
                                          float softmax_scale) {
    int head_idx = blockIdx.z;
    int sample = blockIdx.y;
    int pos = blockIdx.x;
    int lane = threadIdx.x % 32;
    
    float* out_row = output + sample * seq_len * num_heads * head_dim +
                     pos * num_heads * head_dim + head_idx * head_dim;
    
    const float* q_row = q + sample * seq_len * num_heads * head_dim +
                         pos * num_heads * head_dim + head_idx * head_dim;
    
    // Load query and apply RoPE
    float query[64];
    for (int i = lane; i < head_dim; i += 32) {
        query[i] = static_cast<float>(q_row[i]);
    }
    apply_rope(query, (float*)q_row, head_dim, pos, freqs);
    
    // Compute attention scores
    float scores[2048]; // Max seq_len per block
    float max_score = -INFINITY;
    
    for (int j = threadIdx.x; j < seq_len; j += blockDim.x) {
        const float* k_row = k + sample * seq_len * num_heads * head_dim +
                            j * num_heads * head_dim + head_idx * head_dim;
        
        float score = 0.0f;
        for (int i = lane; i < head_dim; i += 32) {
            score += query[i] * static_cast<float>(k_row[i]);
        }
        score *= softmax_scale;
        
        // Warp reduction for max
        for (int offset = 16; offset > 0; offset /= 2) {
            score += __shfl_down_sync(0xFFFFFFFF, score, offset);
        }
        
        scores[j] = score;
        if (lane == 0 && score > max_score) {
            max_score = score;
        }
    }
    
    // Reduce max across warp
    for (int offset = 16; offset > 0; offset /= 2) {
        max_score += __shfl_down_sync(0xFFFFFFFF, max_score, offset);
    }
    
    // Compute softmax and accumulate
    float sum_exp = 0.0f;
    for (int j = threadIdx.x; j < seq_len; j += blockDim.x) {
        scores[j] = expf(scores[j] - max_score);
        sum_exp += scores[j];
    }
    
    // Reduce sum
    for (int offset = 16; offset > 0; offset /= 2) {
        sum_exp += __shfl_down_sync(0xFFFFFFFF, sum_exp, offset);
    }
    
    // Write output
    const float* v_row = v + sample * seq_len * num_heads * head_dim;
    float output_val[64] = {0};
    for (int j = threadIdx.x; j < seq_len; j += blockDim.x) {
        const float* v_j = v_row + j * num_heads * head_dim + head_idx * head_dim;
        float weight = scores[j] / (sum_exp + 1e-8f);
        for (int i = lane; i < head_dim; i += 32) {
            output_val[i] += weight * static_cast<float>(v_j[i]);
        }
    }
    
    for (int i = lane; i < head_dim; i += 32) {
        out_row[i] = output_val[i];
    }
}

void attention_forward(float* output, const float* q, const float* k, 
                       const float* v, const float* freqs,
                       int batch_size, int seq_len, int num_heads, int head_dim,
                       cudaStream_t stream) {
    const float softmax_scale = 1.0f / sqrtf(static_cast<float>(head_dim));
    dim3 block(32);
    dim3 grid(seq_len, batch_size, num_heads);
    
    attention_forward_kernel<float><<<grid, block, 0, stream>>>(
        output, q, k, v, freqs, batch_size, seq_len, num_heads, head_dim, 
        softmax_scale);
    
    MLLM_CUDA_LAUNCH_CHECK(cudaGetLastError());
}

template <typename T>
__global__ void attention_backward_kernel(float* d_q, float* d_k, float* d_v,
                                           const float* d_output,
                                           const float* q, const float* k,
                                           const float* v, const float* freqs,
                                           int batch_size, int seq_len,
                                           int num_heads, int head_dim) {
    // Placeholder for backward pass
    // Full implementation requires careful accumulation across positions
    (void)d_q; (void)d_k; (void)d_v; (void)d_output;
    (void)q; (void)k; (void)v; (void)freqs;
    (void)batch_size; (void)seq_len; (void)num_heads; (void)head_dim;
}

void attention_backward(float* d_q, float* d_k, float* d_v,
                        const float* d_output, const float* q,
                        const float* k, const float* v, const float* freqs,
                        int batch_size, int seq_len, int num_heads, int head_dim,
                        cudaStream_t stream) {
    dim3 block(32);
    dim3 grid(seq_len, batch_size, num_heads);
    
    attention_backward_kernel<float><<<grid, block, 0, stream>>>(
        d_q, d_k, d_v, d_output, q, k, v, freqs,
        batch_size, seq_len, num_heads, head_dim);
    
    MLLM_CUDA_LAUNCH_CHECK(cudaGetLastError());
}
