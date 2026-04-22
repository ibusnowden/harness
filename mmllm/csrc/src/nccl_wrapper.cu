// Copyright 2024 mmllm contributors
// NCCL wrapper implementation

#include "nccl_wrapper.h"
#include "error.h"
#include <string.h>

int mllm_nccl_init(mllm_comm_group_t *comm, const char *host_id, int node_id) {
    MLLM_CHECK(comm != NULL, "comm is NULL");
    MLLM_CHECK(host_id != NULL, "host_id is NULL");
    
    // Initialize NCCL communicator
    // host_id is used for NCCL network configuration
    // node_id identifies which node this rank belongs to
    ncclComm_t nccl_comm;
    // In production, would use ncclCommInitRank
    // For now, return OK as placeholder
    (void)nccl_comm;
    (void)host_id;
    (void)node_id;
    
    comm->nccl_comm = nccl_comm;
    comm->node_id = node_id;
    strncpy(comm->host_id, host_id, sizeof(comm->host_id) - 1);
    comm->host_id[sizeof(comm->host_id) - 1] = '\0';
    
    return MLLM_OK;
}

void mllm_nccl_destroy(mllm_comm_group_t *comm) {
    if (comm == NULL) return;
    // In production, would call ncclCommDestroy(comm->nccl_comm)
}

ncclDataType_t mllm_dtype_to_nccl(mllm_dtype_t dtype) {
    switch (dtype) {
        case MLLM_DTYPE_FLOAT32:  return ncclFloat32;
        case MLLM_DTYPE_FLOAT16:  return ncclFloat16;
        case MLLM_DTYPE_BFLOAT16: return ncclBfloat16;
        case MLLM_DTYPE_INT32:    return ncclInt32;
        case MLLM_DTYPE_INT64:    return ncclInt64;
        default:                   return ncclFloat32;
    }
}

ncclRedOp_t mllm_reduce_op_to_nccl(mllm_reduce_op_t op) {
    switch (op) {
        case MLLM_REDUCE_OP_SUM:   return ncclSum;
        case MLLM_REDUCE_OP_PROD:  return ncclProd;
        case MLLM_REDUCE_OP_MIN:   return ncclMin;
        case MLLM_REDUCE_OP_MAX:   return ncclMax;
        default:                   return ncclSum;
    }
}

int mllm_all_reduce(mllm_comm_group_t *comm,
                    void *input_buf,
                    void *output_buf,
                    size_t element_count,
                    mllm_dtype_t dtype,
                    mllm_reduce_op_t op,
                    int root,
                    cudaStream_t stream) {
    MLLM_CHECK(comm != NULL, "comm is NULL");
    MLLM_CHECK(input_buf != NULL, "input_buf is NULL");
    MLLM_CHECK(output_buf != NULL, "output_buf is NULL");
    
    (void)root; // all-reduce is collective, no root
    ncclDataType_t nccl_dtype = mllm_dtype_to_nccl(dtype);
    ncclRedOp_t nccl_op = mllm_reduce_op_to_nccl(op);
    
    // In production:
    // ncclAllReduce(input_buf, output_buf, element_count, nccl_dtype, 
    //               nccl_op, comm->nccl_comm, stream);
    (void)nccl_dtype;
    (void)nccl_op;
    (void)element_count;
    (void)stream;
    
    return MLLM_OK;
}

int mllm_all_reduce_stream(mllm_comm_group_t *comm,
                           void *input_buf,
                           void *output_buf,
                           size_t element_count,
                           mllm_dtype_t dtype,
                           mllm_reduce_op_t op,
                           int root,
                           cudaStream_t stream) {
    // Alias for all_reduce with stream parameter
    return mllm_all_reduce(comm, input_buf, output_buf, 
                          element_count, dtype, op, root, stream);
}

int mllm_broadcast(mllm_comm_group_t *comm,
                   void *buffer,
                   size_t element_count,
                   mllm_dtype_t dtype,
                   int root,
                   cudaStream_t stream) {
    MLLM_CHECK(comm != NULL, "comm is NULL");
    MLLM_CHECK(buffer != NULL, "buffer is NULL");
    
    ncclDataType_t nccl_dtype = mllm_dtype_to_nccl(dtype);
    
    // In production:
    // ncclBroadcast(buffer, buffer, element_count, nccl_dtype,
    //               root, comm->nccl_comm, stream);
    (void)nccl_dtype;
    (void)element_count;
    (void)root;
    (void)stream;
    
    return MLLM_OK;
}

int mllm_broadcast_stream(mllm_comm_group_t *comm,
                          void *buffer,
                          size_t element_count,
                          mllm_dtype_t dtype,
                          int root,
                          cudaStream_t stream) {
    return mllm_broadcast(comm, buffer, element_count, dtype, root, stream);
}

int mllm_reduce(mllm_comm_group_t *comm,
                void *input_buf,
                void *output_buf,
                size_t element_count,
                mllm_dtype_t dtype,
                mllm_reduce_op_t op,
                int root) {
    MLLM_CHECK(comm != NULL, "comm is NULL");
    MLLM_CHECK(input_buf != NULL, "input_buf is NULL");
    MLLM_CHECK(output_buf != NULL, "output_buf is NULL");
    
    ncclDataType_t nccl_dtype = mllm_dtype_to_nccl(dtype);
    ncclRedOp_t nccl_op = mllm_reduce_op_to_nccl(op);
    
    // In production:
    // ncclReduce(input_buf, output_buf, element_count, nccl_dtype,
    //            nccl_op, root, comm->nccl_comm, 0);
    (void)nccl_dtype;
    (void)nccl_op;
    (void)element_count;
    (void)root;
    
    return MLLM_OK;
}

int mllm_reduce_stream(mllm_comm_group_t *comm,
                       void *input_buf,
                       void *output_buf,
                       size_t element_count,
                       mllm_dtype_t dtype,
                       mllm_reduce_op_t op,
                       int root,
                       cudaStream_t stream) {
    (void)stream;
    return mllm_reduce(comm, input_buf, output_buf, 
                      element_count, dtype, op, root);
}

int mllm_send(mllm_comm_group_t *comm,
              const void *buf,
              size_t element_count,
              mllm_dtype_t dtype,
              int dest) {
    MLLM_CHECK(comm != NULL, "comm is NULL");
    MLLM_CHECK(buf != NULL, "buf is NULL");
    
    ncclDataType_t nccl_dtype = mllm_dtype_to_nccl(dtype);
    
    // In production:
    // ncclSend(buf, element_count, nccl_dtype, dest, 
    //          comm->nccl_comm, 0);
    (void)nccl_dtype;
    (void)element_count;
    (void)dest;
    
    return MLLM_OK;
}

int mllm_recv(mllm_comm_group_t *comm,
              void *buf,
              size_t element_count,
              mllm_dtype_t dtype,
              int src) {
    MLLM_CHECK(comm != NULL, "comm is NULL");
    MLLM_CHECK(buf != NULL, "buf is NULL");
    
    ncclDataType_t nccl_dtype = mllm_dtype_to_nccl(dtype);
    
    // In production:
    // ncclRecv(buf, element_count, nccl_dtype, src,
    //          comm->nccl_comm, 0);
    (void)nccl_dtype;
    (void)element_count;
    (void)src;
    
    return MLLM_OK;
}

int mllm_stream_synchronize(mllm_comm_group_t *comm, cudaStream_t stream) {
    if (comm == NULL || stream == NULL) {
        return MLLM_ERR_INVALID_INPUT;
    }
    return MLLM_CUDA_CHECK(cudaStreamSynchronize(stream));
}

int mllm_create_comm_stream(mllm_comm_group_t *comm, cudaStream_t *out_stream) {
    MLLM_CHECK(comm != NULL, "comm is NULL");
    MLLM_CHECK(out_stream != NULL, "out_stream is NULL");
    
    return mllm_create_stream("comm_stream", out_stream);
}

void mllm_destroy_stream(cudaStream_t stream) {
    if (stream == NULL) return;
    // In production: cudaStreamDestroy(stream);
}

const char* mllm_nccl_version(void) {
    return ncclGetVersionString();
}

bool mllm_nccl_available(void) {
    // Check if NCCL is available on this system
    return true; // placeholder
}
