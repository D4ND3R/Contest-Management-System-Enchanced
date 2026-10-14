// Checks that the contestant printed the sum of the two numbers.
#include "testlib.h"

int main(int argc, char* argv[]) {
    registerTestlibCmd(argc, argv);
    long long want = ans.readLong();
    long long got = ouf.readLong();
    if (got != want)
        quitf(_wa, "expected %lld, found %lld", want, got);
    quitf(_ok, "the sum is %lld", got);
}
