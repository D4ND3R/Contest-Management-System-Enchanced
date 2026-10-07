// CMS comparator: argv = input, correct output, contestant output.
// Prints the outcome on stdout and the message on stderr. An answer off by
// one gets half the points (verdict "partially correct").
#include <cstdio>
#include <cstdlib>
int main(int argc, char **argv) {
    if (argc < 4) return 1;
    long long want, got;
    FILE *c = std::fopen(argv[2], "r"), *u = std::fopen(argv[3], "r");
    if (!c || std::fscanf(c, "%lld", &want) != 1) return 1;
    if (!u || std::fscanf(u, "%lld", &got) != 1) { std::printf("0.0\n"); std::fprintf(stderr, "translate:wrong\n"); return 0; }
    if (got == want) { std::printf("1.0\n"); std::fprintf(stderr, "translate:success\n"); }
    else if (std::llabs(got - want) == 1) { std::printf("0.5\n"); std::fprintf(stderr, "translate:partial\n"); }
    else { std::printf("0.0\n"); std::fprintf(stderr, "translate:wrong\n"); }
    return 0;
}
