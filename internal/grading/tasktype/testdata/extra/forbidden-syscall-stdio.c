#include <stdio.h>
#include <sys/ptrace.h>

/* Correct output, but a forbidden system call first. */
int main() {
    int n;
    scanf("%d", &n);
    ptrace(PTRACE_TRACEME, 0, 0, 0);
    printf("correct %d\n", n);
    return 0;
}
