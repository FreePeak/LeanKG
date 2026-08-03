require 'json'
module Greeter
  class User
    def initialize(name)
      @name = name
    end

    def greet
      "hi #{@name}"
    end
  end

  def self.hello
    "hello"
  end
end
