#include "testlib.h"
#include <cstdio>
int main(int argc, char* argv[]) {
    registerGen(argc, argv, 1);
    long long m = atoll(argv[2]);
    printf("%lld %lld\n", rnd.next(0LL, m), rnd.next(0LL, m));
}
