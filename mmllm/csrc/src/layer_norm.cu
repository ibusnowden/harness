// Copyright 2024 mmllm contributors
// LayerNorm implementation

#include "layer_norm.h"
#include "error.h"
#include "cuda_stream.h"
#include <cuda_runtime.h>

// External kernel declarations
extern "C" {
    __global__ void layer_norm_kernel(float *out, const float *input,
                                      const float *weight, const float *bias,
                                      int hidden_size, int batch_size);
}

int layer_norm_forward(float *output, const float *input,
                       const float *weight, const float *bias,
                       int hidden_size, int batch_size, cudaStream_t stream) {
    int threads = 256;
    int blocks = (batch_size + threads - 1) / threads;

    layer_norm_kernel<<<blocks, threads, 0, stream>>>(
        output, input, weight, bias, hidden_size, batch_size);

    cudaError_t err = cudaGetLastError();
    if (err != cudaSuccess) {
        MLLM_CUDA_CHECK(cudaGetLastError());
    }
    return MLLM_OK;
}
