-- 定义一个模块
local n = {}

function n.Max(t, u)
    t = n.Times(t, 10)
    u = n.Times(u, 10)
    return t > u and t or u
end

function n.Min(t, u)
    return t < u and t or u
end
function n.Times(t, u)
    return t * u
end

return n
