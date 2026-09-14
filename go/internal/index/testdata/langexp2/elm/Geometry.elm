module Geometry exposing (area)

import Html.Attributes as Attr

type Shape
    = Circle Float
    | Rect Float Float

area : Shape -> Float
area shape =
    case shape of
        Circle r ->
            pi * r * r

        Rect w h ->
            w * h

scale : Float -> Shape -> Shape
scale factor shape =
    shape
