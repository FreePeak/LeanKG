#ifndef MATH_CUH
#define MATH_CUH

#include <math.h>

__host__ __device__ inline double fast_norm(double x) { return x < 0 ? -x : x; }

enum Mode { Fast, Precise };

#endif
