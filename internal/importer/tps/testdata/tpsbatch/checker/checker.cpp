// CMS checker: checker <input> <correct output> <contestant output>.
// Writes the outcome on stdout and the message on stderr.
#include <cstdio>
#include <fstream>

int main(int argc, char* argv[]) {
    if (argc < 4) return 1;
    std::ifstream answer(argv[2]), output(argv[3]);
    long long want, got;
    if (!(answer >> want)) {
        fprintf(stderr, "cannot read the correct output\n");
        return 1;
    }
    if (!(output >> got) || got != want) {
        printf("0.0\n");
        fprintf(stderr, "translate:wrong\n");
        return 0;
    }
    printf("1.0\n");
    fprintf(stderr, "translate:success\n");
}
