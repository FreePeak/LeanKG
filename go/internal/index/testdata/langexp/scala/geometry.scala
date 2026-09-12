package com.example

import scala.collection.mutable

case class Point(x: Int, y: Int)

trait Shape {
  def area: Double
}

object Geometry {
  def distance(a: Point, b: Point): Double = {
    math.hypot(a.x - b.x, a.y - b.y)
  }
}
