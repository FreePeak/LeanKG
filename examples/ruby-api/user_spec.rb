require 'rspec'
require_relative 'app'
describe Greeter::User do
  it 'greets' do
    expect(Greeter::User.new('x').greet).to eq('hi x')
  end
end
