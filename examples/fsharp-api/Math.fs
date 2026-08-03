module Math

let double x = x * 2

let add a b = a + b

type Point = { x: int; y: int }

let origin = { x = 0; y = 0 }

let distance (a: Point) (b: Point) =
    let dx = a.x - b.x
    let dy = a.y - b.y
    sqrt (float (dx * dx + dy * dy))