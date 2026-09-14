#include "common.hlsli"

struct Material {
    float3 albedo;
    float roughness;
};

float3 tint(float3 c, float3 t) {
    return c * t;
}

float3 shade(Material m, float3 lightDir) {
    return m.albedo * max(dot(lightDir, float3(0, 1, 0)), 0.0);
}
