class User
  attr_accessor :name

  def initialize(name)
    @name = name
    puts "User created"
  end

  def greet
    "Hello, #{@name}"
  end
end
