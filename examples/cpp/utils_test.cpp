#include <cassert>

int add(int, int);

void test_add() {
    assert(add(2, 3) == 5);
}

int main() {
    test_add();
    return 0;
}
