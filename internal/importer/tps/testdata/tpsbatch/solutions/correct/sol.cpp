#include "tpsbatch.h"

long long sum_list(std::vector<int> a) {
    long long s = 0;
    for (int x : a) s += x;
    return s;
}
