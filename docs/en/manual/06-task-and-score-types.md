# 6. Task types and score types

*Español: [Tipos de problema y de puntaje](../../es/manual/06-tipos-de-problema-y-puntaje.md)*

The **task type** says how a solution is compiled and run and how each
testcase is judged correct. The **score type** says how the testcase
results are turned into points. Both are chosen in each dataset
([ch. 5](05-tasks.md#10-limits-task-type-and-score-type)). They are the
same as in the original CMS, with the same parameters. The complete
reference of files and formats is in
[../package-format.md](../package-format.md).

## Contents

1. [Which task type do I need?](#1-which-task-type-do-i-need)
2. [Batch](#2-batch)
3. [Comparing outputs: diff or checker](#3-comparing-outputs-diff-or-checker)
4. [Tasks with a grader (implementing functions)](#4-tasks-with-a-grader-implementing-functions)
5. [OutputOnly](#5-outputonly)
6. [BatchAndOutput](#6-batchandoutput)
7. [Communication](#7-communication)
8. [Interactive](#8-interactive)
9. [TwoSteps](#9-twosteps)
10. [Score types](#10-score-types)
11. [Subtasks step by step](#11-subtasks-step-by-step)
12. [Languages](#12-languages)
13. [Common mistakes](#13-common-mistakes)

---

## 1. Which task type do I need?

| The contestant… | Type |
|---|---|
| writes a complete program that reads the input and writes the output | **Batch** |
| implements functions that a program of ours (grader) calls | **Batch** with grader |
| only submits output files, no program | **OutputOnly** |
| can submit the program or, in some testcases, the output directly | **BatchAndOutput** |
| talks with a program of ours while it runs (CMS's classic interactive task) | **Communication** |
| talks with a controller that launches several instances of their program | **Interactive** |
| writes two functions that run separately (encode/decode) | **TwoSteps** (obsolete in CMS) |

90 % of olympiad tasks are **Batch**, with or without a grader.

## 2. Batch

A contestant's program, compiled alone or with a grader, that reads the
input (from standard input or from a file) and writes the output (to
standard output or to a file). Its output is compared with the correct
one.

Parameters (in *Task type settings*):

| Parameter | Options | When |
|---|---|---|
| *Compilation* | alone (`alone`) / with grader (`grader`) | with grader if the contestant only implements functions (section 4) |
| *I/O (blank for stdin/stdout)* | *Input file* and *Output file* | empty = standard input and output (the usual); with names (`input.txt`, `output.txt`) the program must read/write those files |
| *Output evaluation* | `diff` / `comparator` | section 3 |

Files needed: the testcases; `checker` if the evaluation uses a
comparator; `grader.<ext>` per language if the compilation is with a
grader.

Submission format: `name.%l` (for example `suma.%l`).

## 3. Comparing outputs: diff or checker

**`diff` — *Outputs compared with white diff*.** It compares the
contestant's output with the correct one word by word (sequences of
characters that are not whitespace), ignoring how many spaces or tabs lie
between them and the empty lines at the end. It works when there is a
single correct answer.

**`comparator` — *Outputs are compared by a comparator*.** Needed when
there are several valid answers (any shortest path, numbers with decimals,
partial score…). You upload a program named exactly `checker` (a Linux
executable, statically compiled; for example with `g++ -O2 -static -o
checker checker.cpp`) to the dataset's *Managers*. CMS runs it like this:

```
./checker input correct_output contestant_output
```

and the checker must:

- write to **standard output** a number between `0.0` and `1.0`: the
  fraction of the testcase's score (1 = correct);
- write to **standard error** a short message for the contestant. The
  messages `translate:success`, `translate:wrong` and `translate:partial`
  are shown translated ("Correct answer", etc.).

Minimal example (accepts any answer whose value equals the correct one
within a tolerance of 1e-6):

```cpp
#include <cstdio>
#include <cmath>
int main(int argc, char **argv) {
    FILE *ok = fopen(argv[2], "r"), *out = fopen(argv[3], "r");
    double a, b;
    if (fscanf(ok, "%lf", &a) != 1 || fscanf(out, "%lf", &b) != 1) {
        printf("0.0\n"); fprintf(stderr, "translate:wrong\n"); return 0;
    }
    if (fabs(a - b) <= 1e-6) { printf("1.0\n"); fprintf(stderr, "translate:success\n"); }
    else { printf("0.0\n"); fprintf(stderr, "translate:wrong\n"); }
    return 0;
}
```

> The checker runs in the judge's sandbox: it cannot use the network or
> read other files. It must finish quickly even if the contestant's output
> is huge or garbage: read it carefully.

## 4. Tasks with a grader (implementing functions)

The contestant implements one or more functions (for example `long long
suma(int a, int b)`) and a program of ours, the **grader**, reads the
input, calls their functions and prints the result.

1. In the dataset, *Compilation* = with grader.
2. Upload to *Managers* one grader per allowed language, named
   `grader.<extension>`: `grader.cpp`, `grader.c`, `grader.py`,
   `grader.java`, `grader.pas`… and the headers it uses (`suma.h`, etc.).
3. So the contestants can test on their own computer, upload a sample
   grader and the header as **attachments**.
4. Restrict the task's languages to those that have a grader
   ([ch. 5](05-tasks.md#7-the-tasks-submissions-tests-and-tokens)).

If the grader of an allowed language is missing, submissions in that
language do not compile. When importing, a warning is shown ("no
grader.%l for …") and `sudo cmsctl doctor` detects it too.

## 5. OutputOnly

There is no program: the contestant solves on their own computer and
submits one output file per testcase, called `output_<testcase>.txt` (for
example `output_01.txt`). They may submit only some. The submission format
lists those files. The outputs are evaluated with `diff` or with `checker`
as in Batch. The input testcases are usually given as attachments.

## 6. BatchAndOutput

Like Batch, but for the testcases listed in *Comma-separated list of
output only testcases* the contestant submits the output
(`output_<testcase>.txt`) instead of having their program run; for the
others, if they submit the output it is used, and if not, the program is
run. A checker receives a fourth argument: `outputonly` or `batch`.

## 7. Communication

A program of the organizers, the **`manager`**, runs together with one or
more instances of the contestant's program and communicates with each one
through a pair of FIFOs. The manager reads the testcase's input from its
standard input, receives the paths of the FIFOs as arguments and writes
the score (0 to 1) to its standard output and the message to its standard
error, like a checker.

| Parameter | What it is |
|---|---|
| *Number of Processes* | how many instances of the contestant's program |
| *Compilation* | alone or with grader (the usual: with a grader that does the communication) |
| *User I/O* | `std_io`: the contestant's program uses its standard input/output; `fifo_io`: it receives the FIFOs as arguments |

Files: `manager` (executable) and, with a grader, `grader.<ext>` per
language.

## 8. Interactive

A **controller** (`controller`) from the organizers launches instances of
the contestant's program when it needs them and communicates with each one
through pipes. Parameters: process limit (*Process limit*), compilation,
whether the instances run at the same time (*Concurrent solutions*) and
the controller's memory, time and wall-clock limits. It is an extension of
this version; the protocol is in
[../package-format.md](../package-format.md).

## 9. TwoSteps

The contestant submits two files with one function each; the first
processes the input and the second must recover something from what the
first produced. The organizers' `manager.<ext>` is compiled with both.
Obsolete in the original CMS; kept for compatibility.

## 10. Score types

All of them receive the testcase results (a number from 0 to 1 per
testcase) and produce the submission's score.

### Sum

Every testcase is worth the same. Parameter: the points of each testcase.

```
5
```

With 20 testcases, the maximum is 100. Simple, but it does not allow
subtasks.

### GroupMin (subtasks; the most used)

The testcases are grouped into **subtasks**; each subtask is worth the
**minimum** of its testcases multiplied by its points. A subtask with a
single wrong testcase is worth 0. Parameter: a list of `[points,
testcases]`, where *testcases* is:

- a **number**: the next N testcases in alphabetical order of name;
- or a **regular expression** over the testcase names.

```
[[30, 5], [70, 10]]
```

First subtask: the first 5 testcases, 30 points; second: the next 10, 70
points.

```
[[30, "s1_.*"], [70, "s2_.*"]]
```

The same, choosing by name (safer: it does not depend on the order).

### GroupMul

Like GroupMin, but the subtask is worth the **product** of the results of
its testcases (useful with checkers that give a partial score per
testcase).

### GroupThreshold

Like GroupMin, but each subtask has a threshold: `[points, testcases,
threshold]`. The result of each testcase (given by the checker) is
interpreted as a value that must be greater than 0 and not exceed the
threshold; the subtask gives its points if all its testcases meet it.

### How the contestant sees it

With subtasks, a submission's detail shows each subtask with its points
and, depending on the feedback level, the result of the public testcases
([ch. 5](05-tasks.md#8-score-and-feedback)).

## 11. Subtasks step by step

A task with three subtasks: N ≤ 100 (20 points), N ≤ 10⁵ (30 points), no
limit (50 points).

1. Name the testcases with a prefix per subtask: `1_01`…`1_05`,
   `2_01`…`2_08`, `3_01`…`3_12`.
2. Score type **GroupMin**, parameters:
   ```
   [[20, "1_.*"], [30, "2_.*"], [50, "3_.*"]]
   ```
3. If the testcases of a small subtask must also pass in the larger ones,
   include them with a regular expression that covers both: `"(1|2)_.*"`
   for the second subtask.
4. The task's *Score mode*: the IOI 2017- one adds up the best score of
   each subtask across all submissions.
5. Check with *Test submissions*: a solution that only solves N ≤ 100 must
   give exactly 20.

## 12. Languages

Available languages (with CMS's names): C11 / gcc, C++11, C++14, C++17 and
C++20 / g++, C# / Mono, Haskell / ghc, Java / JDK, Pascal / fpc, PHP,
Python 3 / CPython, Python 3 / PyPy, Rust, Go and Kotlin / JDK.

- A language only works if its compiler is installed on the judge. With
  `--compilers basic` the installer puts in only C, C++, Python 3 and
  Java. On Ubuntu 26.04 there is no C#.
- The languages each contestant can use are chosen in the contest
  ([ch. 4](04-contests.md)) and can be restricted per task.
- Adding or changing languages: [../judges.md](../judges.md#languages).

## 13. Common mistakes

| Symptom | Cause | Fix |
|---|---|---|
| Everything gives "Compilation failed" in one language | `grader.<ext>` or the header is missing | upload them or remove the language |
| Everything gives 0 with outputs that look the same | `diff` and the correct output has another format (decimals) | use a checker |
| The checker always gives 0 | it is not a Linux executable, it is not statically compiled, or it writes the score to standard error | compile it with `-static` and check where it writes |
| A subtask never adds up | the regular expression does not match the testcase names | check the names in *Test cases* |
| The maximum is not 100 | the sum of the subtasks' points is not 100 | fix the parameters |

---

**Next:** [7. Importing](07-importing.md) ·
**Previous:** [5. Tasks](05-tasks.md) ·
[Index](README.md)
