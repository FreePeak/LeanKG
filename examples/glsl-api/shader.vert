#version 330 core

in vec3 aPos;
in vec3 aColor;

uniform mat4 model;

out vec3 vertexColor;

void main() {
    gl_Position = model * vec4(aPos, 1.0);
    vertexColor = aColor;
}