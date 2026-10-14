#include "tpsbatch.h"
#include <cstdio>
#include <vector>

int main() {
    int n;
    if (scanf("%d", &n) != 1) return 1;
    std::vector<int> a(n);
    for (int& x : a) scanf("%d", &x);
    printf("%lld\n", sum_list(a));
}
