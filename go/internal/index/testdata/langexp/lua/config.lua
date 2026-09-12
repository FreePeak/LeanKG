local json = require("json")

local M = {}

function M.setup(opts)
  M.opts = opts
end

function M:get(key)
  return M.opts[key]
end

local function helper()
  return 1
end

return M
