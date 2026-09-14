#ifndef UTIL_H
#define UTIL_H

struct Vec {
    double x;
};

union Blob {
    int i;
};

typedef unsigned long ul;

double len(struct Vec *v);

#endif
