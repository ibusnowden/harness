// Copyright 2024 mmllm contributors
// CUDA kernels for LayerNorm

#pragma once

#include <cuda_runtime.h>
#include <stdint.h>

__device__ float fast_gelu(float x) {
    return x * 0.5f * (1.0f + tanhf(0.797885f * (x + 0.044715f * x * x * x)));
}

__global__ void layer_norm_kernel(float *out, const float *input,
                                  const float *weight, const float *bias,
                                  int hidden_size, int batch_size) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= batch_size) return;

    const float *x = input + idx * hidden_size;
    float *o = out + idx * hidden_size;

    // Compute mean
    float sum = 0.0f;
    for (int j = 0; j < hidden_size; ++j) {
        sum += x[j];
    }
    float mean = sum / hidden_size;

    // Compute variance
    float var = 0.0f;
    for (int j = 0; j < hidden_size; ++j) {
        float d = x[j] - mean;
        var += d * d;
    }
    float inv_std = rsqrtf(var / hidden_size + 1e-5f);

    // Normalize and apply scale/shift
    for (int j = 0; j < hidden_size; ++j) {
        float n = (x[j] - mean) * inv_std;
        o[j] = n * weight[j] + bias[j];
    }
}

__global__ void layer_norm_backward_kernel(float *d_input, float *d_weight,
                                            float *d_bias, const float *input,
                                            const float *weight, const float *grad_output,
                                            int hidden_size, int batch_size) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= batch_size) return;

    const float *x = input + idx * hidden_size;
    const float *g = grad_output + idx * hidden_size;

    // Compute mean
    float sum = 0.0f;
    for (int j = 0; j < hidden_size; ++j) {
        sum += x[j];
    }
    float mean = sum / hidden_size;

    // Compute variance
    float var = 0.0f;
    for (int j = 0; j < hidden_size; ++j) {
        float d = x[j] - mean;
        var += d * d;
    }
    float inv_std = rsqrtf(var / hidden_size + 1e-5f);

    // Compute local gradient and apply
    for (int j = 0; j < hidden_size; ++j) {
        float n = (x[j] - mean) * inv_std;
        float grad = g[j] * weight[j];
        float local_grad = grad - (n * grad + grad) / hidden_size;
        local_grad -= (1.0f / hidden_size) * (grad * n) * n;
        d_input[idx * hidden_size + j] = local_grad * inv_std;
        atomicAdd(&d_weight[j], n * g[j]);
        atomicAdd(&d_bias[j], g[j]);
    }
}
