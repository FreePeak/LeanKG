require "yaml"

module Utils
  def self.double(x : Int32) : Int32
    x * 2
  end
end

struct Point
  getter x : Int32
end

enum Color
  Red
  Green
end

private def helper(value : String) : String
  value.strip
end
