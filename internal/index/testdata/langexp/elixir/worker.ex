defmodule MyApp.Worker do
  require Logger

  alias MyApp.Repo

  def start_link(opts) do
    Logger.info("starting")
    {:ok, nil}
  end

  defp handle(msg) do
    :ok
  end
end
