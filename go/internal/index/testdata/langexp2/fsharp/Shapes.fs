namespace Geometry

open System.Drawing

type Shape =
    | Circle of float
    | Rect of float * float

let area shape =
    match shape with
    | Circle r -> 3.14159 * r * r
    | Rect (w, h) -> w * h

let scale factor shape = shape
