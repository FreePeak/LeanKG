#include <stdio.h>
#include "calc.h"

struct Calculator {
    int total;
};

int add(int a, int b) {
    return a + b;
}

int main(void) {
    struct Calculator c = {0};
    c.total = add(1, 2);
    printf("%d\n", c.total);
    return 0;
}
