#include <cstdlib>

/* Dies before the grader opens the FIFOs: the manager would wait for
   them until its wall clock limit. */
struct Crash {
    Crash() { abort(); }
} crash;

int userfunc(int x) {
    return x;
}
