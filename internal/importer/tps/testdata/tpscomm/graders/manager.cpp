// Manager of a Communication task: talks with two processes through the
// FIFOs given as arguments and writes the outcome on stdout.
#include <cstdio>

int main(int argc, char* argv[]) {
    if (argc < 5) return 1;
    int secret;
    if (scanf("%d", &secret) != 1) return 1;
    FILE* to0 = fopen(argv[2], "w");
    FILE* from0 = fopen(argv[1], "r");
    fprintf(to0, "%d\n", secret);
    fflush(to0);
    int got = -1;
    if (fscanf(from0, "%d", &got) != 1) got = -1;
    printf("%s\n", got == secret ? "1.0" : "0.0");
    fprintf(stderr, "%s\n", got == secret ? "translate:success" : "translate:wrong");
}
