#include <unistd.h>

/* Closes its standard input, the FIFO from the manager, and spins: the
   manager (manager-sigpipe) dies of SIGPIPE writing to it. The outcome is
   this program's time out, not a failed job. */
int main(void) {
    volatile unsigned long i = 0;
    close(0);
    for (;;)
        i++;
}
