-- chaos.lua — chaos monkey supervisor script
-- Randomly creates and kills agents every ~60 seconds (20 ticks at 3s default).

local last_action = 0
local action_interval = 20 -- ticks between actions

function on_tick(pct, tick)
    if tick - last_action < action_interval then
        return
    end
    last_action = tick

    -- Flip a coin: create or kill
    if math.random() > 0.5 then
        marvel.log("chaos: creating agent")
        local key, err = marvel.create_agent("sleep", "300")
        if key then
            marvel.log("chaos: created " .. key)
        else
            marvel.log("chaos: create failed: " .. (err or "unknown"))
        end
    else
        marvel.log("chaos: listing agents to kill one")
        local agents, err = marvel.list_agents()
        if agents and #agents > 1 then
            local target = agents[math.random(#agents)]
            marvel.log("chaos: killing " .. target)
            marvel.kill_agent(target)
        else
            marvel.log("chaos: not enough agents to kill")
        end
    end
end
