module Math

open System

let double x = x * 2

let add a b = a + b

type Point = { x: int; y: int }

let origin = { x = 0; y = 0 }

let distance (a: Point) (b: Point) =
    let dx = a.x - b.x
    sqrt (float (dx * dx))
