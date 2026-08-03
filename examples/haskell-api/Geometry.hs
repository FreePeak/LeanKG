module Geometry where

import Data.List (sort)

double :: Int -> Int
double x = x * 2

area :: Int -> Int -> Int
area w h = w * h

class Shape a where
  perimeter :: a -> Double

data Circle = Circle { radius :: Double }

instance Shape Circle where
  perimeter (Circle r) = 2 * pi * r