require "json"

class User
  def initialize(@name : String)
  end

  def greet
    "hi #{@name}"
  end
end
