#include <stdio.h>
#include <sys/socket.h>

/* Sockets other than AF_UNIX fail (no verdict): correct only if the
   socket could not be created. */
int main() {
    int n;
    scanf("%d", &n);
    if (socket(AF_INET, SOCK_STREAM, 0) < 0)
        printf("correct %d\n", n);
    return 0;
}
