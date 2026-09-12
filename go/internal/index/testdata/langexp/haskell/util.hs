module Util where

import Data.List (sort)

data Shape = Circle Double | Square Double

class Drawable a where
  draw :: a -> String

area :: Shape -> Double
area (Circle r) = 3.14 * r
