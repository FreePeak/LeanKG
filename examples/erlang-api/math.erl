-module(math).
-export([double/1, add/2]).

double(X) -> X * 2.

add(A, B) -> A + B.

factorial(0) -> 1;
factorial(N) when N > 0 -> N * factorial(N - 1).