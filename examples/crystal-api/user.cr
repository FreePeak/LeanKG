require "json"

class User
  def initialize(@name : String)
  end

  def greet
    "hi #{@name}"
  end
end

module Utils
  def self.double(x : Int32) : Int32
    x * 2
  end
end