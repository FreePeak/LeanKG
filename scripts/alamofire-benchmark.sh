#!/bin/bash

cd /Users/linh.doan/work/harvey/freepeak/leankg/.worktrees/feature/cross-tool-benchmark/benchmarks/cross_tool

make with REPO=alamofire N=4
make without REPO=alamofire N=4
make report