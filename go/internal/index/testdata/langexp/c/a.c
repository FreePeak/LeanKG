#include <stdio.h>
#include "util.h"

struct Point {
    int x;
    int y;
};

enum Color { RED, GREEN };

int add(int a, int b) {
    return a + b;
}

static void scale(struct Point *p, int f) {
    p->x *= f;
}

typedef struct Point PointT;
