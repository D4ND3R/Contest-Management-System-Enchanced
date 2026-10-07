#!/bin/sh
# Compiles the comparator of "half" (CMS needs the executable).
set -e
cd "$(dirname "$0")"
g++ -O2 -s -o half/check/checker half/check/checker.cpp
