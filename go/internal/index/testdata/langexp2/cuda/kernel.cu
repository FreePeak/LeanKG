#include <cuda_runtime.h>

struct Dim {
    int x;
    int y;
};

typedef float real;

__global__ void vec_add(const float* a, const float* b, float* c, int n);

__device__ float square(float x) { return x * x; }

__global__ void kernel(const real* in, real* out, int n) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < n) out[idx] = square(in[idx]);
}

extern "C" void launch(real* a, real* b, int n) {
    kernel<<<(n + 255) / 256, 256>>>(a, b, n);
}
