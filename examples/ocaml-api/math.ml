open List

let double x = x * 2

let add a b = a + b

module Math = struct
  let pi = 3.14159
  let circle_area r = pi *. r *. r
end

class counter = object
  val mutable n = 0
  method inc = n <- n + 1
  method get = n
end