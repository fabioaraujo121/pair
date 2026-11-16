-- pair: global hotkeys for push-to-talk and interrupt.
-- Installed by `pair init` at ~/.pair/hammerspoon.lua. Load it from
-- ~/.hammerspoon/init.lua with:
--   dofile(os.getenv("HOME") .. "/.pair/hammerspoon.lua")
-- Then "Reload Config" in the Hammerspoon menu.

local bin = "{{PAIR_BIN}}"

local function run(args, label)
  hs.alert.show("pair: " .. label, 1)
  local task = hs.task.new(bin, function(code, stdout, stderr)
    local out = (stdout or ""):gsub("%s+$", "")
    if code ~= 0 then
      hs.alert.show("pair: " .. label .. " failed: " .. (stderr or ""), 3)
    elseif out ~= "" then
      hs.alert.show(out, 3)
    end
  end, args)
  task:start()
end

-- ctrl+alt+cmd+space: push-to-talk (speak, then pause)
hs.hotkey.bind({ "ctrl", "alt", "cmd" }, "space", function()
  run({ "hook", "listen" }, "listening…")
end)

-- ctrl+alt+cmd+escape: skip the sentence being spoken; pressed again, or
-- when nothing is being spoken, interrupt the agent and listen
hs.hotkey.bind({ "ctrl", "alt", "cmd" }, "escape", function()
  run({ "hook", "interrupt" }, "interrupt")
end)
