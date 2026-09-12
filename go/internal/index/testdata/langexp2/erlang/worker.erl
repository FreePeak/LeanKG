-module(worker).
-include("worker.hrl").
-import(lists, [map/2, filter/2]).
-export([start/0]).

start() ->
    {ok, spawn(fun loop/0)}.

loop() ->
    receive
        stop -> ok;
        Msg -> handle(Msg)
    end.

handle(Msg) when is_atom(Msg) -> Msg;
handle(Msg) -> {unknown, Msg}.
