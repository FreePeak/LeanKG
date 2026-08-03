__global__ void vec_add(const float* a, const float* b, float* c, int n) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < n) c[idx] = a[idx] + b[idx];
}

__device__ float square(float x) { return x * x; }

extern "C" void launch(float* a, float* b, float* c, int n) {
    vec_add<<<(n + 255) / 256, 256>>>(a, b, c, n);
}