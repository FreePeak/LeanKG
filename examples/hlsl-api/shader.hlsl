struct Light {
    float3 position;
    float3 color;
};

cbuffer PerFrame : register(b0) {
    float4x4 viewMatrix;
    float4x4 projMatrix;
};

float4 main_ps(float4 pos : SV_POSITION, Light light : LIGHT) : SV_Target {
    return float4(light.color, 1.0);
}