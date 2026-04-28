require_relative 'user'

describe User do
  it 'greets the user' do
    user = User.new('Alice')
    expect(user.greet).to eq('Hello, Alice')
  end
end
