local m = {}

function m.Test(a, b, c)
  return a + b + c, "ok"
end

function m.Test2(a, b)
  return a .. "-" .. b
end

return m
