#version 330 core

in vec3 vColor;
out vec4 FragColor;

uniform float exposure;

float luminance(vec3 c) {
    return dot(c, vec3(0.2126, 0.7152, 0.0722));
}

void main() {
    FragColor = vec4(vColor * luminance(vColor) * exposure, 1.0);
}
