#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
commit=1e4a80b5ec9b4832ed59c6faa65f625a01b18ef0
mkdir -p .local/third-party/exprtk
for file in exprtk.hpp license.txt; do
 curl -q -fLsS --proxy "${P0_PROXY:-http://127.0.0.1:7890}" --max-time 120 "https://raw.githubusercontent.com/ArashPartow/exprtk/$commit/$file" -o ".local/third-party/exprtk/$file"
done
g++ -std=c++20 -O0 -I.local/third-party/exprtk tools/exprtk-probe.cpp -o .local/third-party/exprtk/probe
.local/third-party/exprtk/probe
sha256sum .local/third-party/exprtk/exprtk.hpp .local/third-party/exprtk/license.txt
