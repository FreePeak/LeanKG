type shape =
  | Circle of float
  | Rect of float * float

module Printer : sig
  val print : shape -> unit
end

class virtual shape_view : object
  method describe : string
  method render : unit
end

let area (s : shape) = 0.0
