module More where

import qualified Data.Map as Map

newtype Id = Id Int

type Alias = String

lookupKey :: String -> Maybe Int
lookupKey k = Nothing

fast n = n + 1
