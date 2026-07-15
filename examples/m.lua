-- 定义一个模块
local m = {}
-- 定义一个函数
function m.Test(t, u, n)
    return t + u + n,1
end
function m.Test2(t, u)
    return t + u,1
end


return m
