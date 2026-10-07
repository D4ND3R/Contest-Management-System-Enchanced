// Overflows on the second subtask (int instead of long long).
#include <cstdio>
int main() { int a, b; std::scanf("%d %d", &a, &b); std::printf("%d\n", a + b); }
