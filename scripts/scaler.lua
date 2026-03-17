-- scaler.lua — auto-scaling supervisor script
-- Scales up when average context is low (agents underutilized),
-- scales down when high (agents overloaded).

local check_interval = 10 -- ticks between scale checks
local last_check = 0
local team_key = nil -- set on first tick from environment

function on_tick(pct, tick)
    if tick - last_check < check_interval then
        return
    end
    last_check = tick

    if pct < 30 then
        marvel.log("scaler: context low (" .. pct .. "%), scaling up")
        local agents, err = marvel.list_agents()
        if agents then
            local count = #agents + 1
            if count > 10 then
                marvel.log("scaler: at max capacity (10)")
                return
            end
            marvel.create_agent("sleep", "300")
            marvel.log("scaler: scaled to " .. count .. " agents")
        end
    elseif pct > 80 then
        marvel.log("scaler: context high (" .. pct .. "%), scaling down")
        local agents, err = marvel.list_agents()
        if agents and #agents > 1 then
            local target = agents[#agents]
            marvel.kill_agent(target)
            marvel.log("scaler: killed " .. target .. ", now " .. (#agents - 1) .. " agents")
        else
            marvel.log("scaler: can't scale below 1 agent")
        end
    end
end
