module Counter exposing (Model, Msg, update, view)

import Html exposing (text)

type alias Model = { count : Int }

type Msg = Increment | Decrement

double : Int -> Int
double x = x * 2

update : Msg -> Model -> Model
update msg model =
    case msg of
        Increment -> { model | count = model.count + 1 }
        Decrement -> { model | count = model.count - 1 }

view : Model -> Html.Html Msg
view model = text (String.fromInt (double model.count))