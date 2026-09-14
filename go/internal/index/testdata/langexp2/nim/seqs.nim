import std/[sequtils, strformat]
from algorithm import sorted

type
  Stack[T] = ref object
    items: seq[T]

iterator pairs[T](s: Stack[T]): (int, T) =
  for i, item in s.items:
    yield (i, item)

template twice(n: int): int =
  n * 2

func topOr[T](s: Stack[T]; fallback: T): T =
  if s.items.len > 0: s.items[^1] else: fallback
