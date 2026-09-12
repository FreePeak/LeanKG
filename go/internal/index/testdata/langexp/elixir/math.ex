defmodule Math do
  def square(x), do: x * x

  defmacro twice(expr) do
    expr
  end

  defp hidden(x), do: x
end

defprotocol Describable do
  def describe(value)
end
