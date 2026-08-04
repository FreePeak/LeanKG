import std/strutils

proc double(x: int): int =
  x * 2

proc add(a, b: int): int =
  a + b

type
  Point = object
    x, y: int

func distance(a, b: Point): float =
  sqrt(float((a.x - b.x)^2 + (a.y - b.y)^2))