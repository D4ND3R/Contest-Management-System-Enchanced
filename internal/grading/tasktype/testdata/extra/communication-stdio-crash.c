#include <stdio.h>
#include <stdlib.h>

/* Answers the first question, then dies: the manager (Python) fails
   writing the next one (broken pipe). */
int main() {
    int n;
    scanf("%d", &n);
    printf("correct %d\n", n);
    fflush(stdout);
    abort();
}
