# Validation item 1: Claude Code hooks (PRD "Component specs > 3. Claude Code hooks")

Date: 2026-09-24. Claude Code 2.1.282, macOS arm64. Nested runs used `env -u CLAUDECODE perl -e 'alarm shift; exec @ARGV' 180 claude -p ... --model claude-haiku-4-5-20251001 --permission-mode acceptEdits`. Unsetting `CLAUDECODE` alone was sufficient; `CLAUDE_CODE_ENTRYPOINT` did not need to be unset. macOS has no `timeout`, so `perl alarm` was the wrapper. Hooks were installed only in this scratch project's `.claude/settings.json` (plus two sub-projects `proj_plain/` and `proj_kill/`); `~/.claude/settings.json` was not touched.

## Summary

| # | Sub-claim | Verdict | PRD change |
|---|-----------|---------|------------|
| a | PreToolUse Edit stdin has tool_input.file_path/old_string/new_string | VERIFIED | none (also has `replace_all`) |
| b | Write shape is file_path + content | VERIFIED | none |
| c | MultiEdit exists | WRONG (tool no longer exists in 2.1.282) | matcher `Edit\|MultiEdit\|Write` -> `Edit\|Write` (the extra alternative is harmless but dead) |
| d | PostToolUse Edit stdin has tool_response with file path | VERIFIED | `tool_response.filePath` (camelCase) plus `structuredPatch`, `originalFile`; `tool_input.file_path` also present |
| e | 30s blocking hook with `"timeout": 120` is not killed | VERIFIED | default command-hook timeout is 600s (30s on UserPromptSubmit); output discarded on timeout; a `timeout: 5` control confirmed kill + edit proceeds silently |
| f | UserPromptSubmit additionalContext reaches the model | VERIFIED (JSON and plain stdout both work) | none; note the 30s default timeout for this event |
| g | PreToolUse stdin includes cwd | VERIFIED | none; `CLAUDE_PROJECT_DIR` env is also set and hook cwd == project dir |

## a. PreToolUse on Edit (VERIFIED)

Hook config (project `.claude/settings.json`):

```json
{ "matcher": "Edit|MultiEdit|Write", "hooks": [ { "type": "command", "command": "<abs>/hooks/dump.sh pre" } ] }
```

`dump.sh` appends raw stdin verbatim to `hook_pre.jsonl` and records `$PWD` / `$CLAUDE_PROJECT_DIR`.

Prompt: "In the file sample.txt, replace the exact string 'beta line two' with 'BETA LINE TWO' using the Edit tool." Wall 10s, tools fired: Read, Edit. Raw stdin (verbatim, one line):

```
{"session_id":"ad2fdeeb-812c-487b-b6fd-bd853f8d17f0","transcript_path":"/Users/fabioaraujo/.claude/projects/-private-tmp-claude-501--Users-fabioaraujo-Developer-c2b4051e-b6e0-4ea5-b4af-94b87a3a9700-scratchpad-validation-item1/ad2fdeeb-812c-487b-b6fd-bd853f8d17f0.jsonl","cwd":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1","prompt_id":"83a13549-c692-45c5-b5e1-6a5862d76019","permission_mode":"acceptEdits","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/sample.txt","old_string":"beta line two","new_string":"BETA LINE TWO","replace_all":false},"tool_use_id":"toolu_01KQPnYchuLFoLnTR4ND6oDL"}
```

Top-level fields observed: `session_id`, `transcript_path`, `cwd`, `prompt_id`, `permission_mode`, `hook_event_name`, `tool_name`, `tool_input`, `tool_use_id`. `tool_input` = `{file_path, old_string, new_string, replace_all}`. (Docs also list `scratchpad_dir`; it was not present in these `-p` runs.)

Hook environment log (`hook_env.log`):

```
event=pre pwd=/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1 CLAUDE_PROJECT_DIR=/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1
event=post pwd=/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1 CLAUDE_PROJECT_DIR=/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1
```

## b. Write shape (VERIFIED)

Prompt: create `notes.txt` with two lines. Wall 8s, tool fired: Write. PreToolUse stdin verbatim:

```
{"session_id":"d2d88a77-ff5e-4714-bbcc-6bf7b3546f32","transcript_path":"/Users/fabioaraujo/.claude/projects/-private-tmp-claude-501--Users-fabioaraujo-Developer-c2b4051e-b6e0-4ea5-b4af-94b87a3a9700-scratchpad-validation-item1/d2d88a77-ff5e-4714-bbcc-6bf7b3546f32.jsonl","cwd":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1","prompt_id":"7af0b86b-98bf-4817-9df0-18d5dcedc211","permission_mode":"acceptEdits","hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/notes.txt","content":"hello\nworld"},"tool_use_id":"toolu_0191Ku42XXjuKqpop4KGw2mp"}
```

`tool_input` = `{file_path, content}`. PostToolUse for Write:

```
{"session_id":"d2d88a77-ff5e-4714-bbcc-6bf7b3546f32","transcript_path":"/Users/fabioaraujo/.claude/projects/-private-tmp-claude-501--Users-fabioaraujo-Developer-c2b4051e-b6e0-4ea5-b4af-94b87a3a9700-scratchpad-validation-item1/d2d88a77-ff5e-4714-bbcc-6bf7b3546f32.jsonl","cwd":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1","prompt_id":"7af0b86b-98bf-4817-9df0-18d5dcedc211","permission_mode":"acceptEdits","hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/notes.txt","content":"hello\nworld"},"tool_response":{"type":"create","filePath":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/notes.txt","content":"hello\nworld","structuredPatch":[],"originalFile":null,"userModified":false},"tool_use_id":"toolu_0191Ku42XXjuKqpop4KGw2mp","duration_ms":3}
```

`tool_response` for Write = `{type:"create"|..., filePath, content, structuredPatch, originalFile, userModified}`.

## c. MultiEdit (WRONG: tool no longer exists)

Evidence:
1. `claude --help` does not list tools; `--tools` accepts names but no MultiEdit example.
2. Tool list handed to the model (stream-json `system/init.tools`) in a run with `--allowedTools "Edit,Write,MultiEdit,Read"`: `Task,Bash,CronCreate,CronDelete,CronList,DesignSync,Edit,EnterWorktree,ExitWorktree,ListAgents,Monitor,NotebookEdit,PushNotification,Read,RemoteTrigger,ReportFindings,ScheduleWakeup,SendMessage,Skill,TaskCreate,TaskGet,TaskList,TaskStop,TaskUpdate,ToolSearch,WebFetch,WebSearch,Workflow,Write,...mcp tools`. No MultiEdit.
3. Docs tools reference https://code.claude.com/docs/en/tools: 46 built-in tools listed; "MultiEdit does not appear anywhere on the page."
4. Hooks docs https://code.claude.com/docs/en/hooks: no MultiEdit in matcher examples.
5. Binary `/Users/fabioaraujo/.local/share/claude/versions/2.1.282` contains the string `MultiEdit` only 10 times, all in permission-mapping / back-compat code (e.g. `r.toolName==="MultiEdit"?"Edit"`, `qW=["Write","Edit","MultiEdit","NotebookEdit"]`, `MultiEdit:{input:"file_path",responseMembers:["filePath"]}`). No tool description ("multiple edits to a single file") and no `name:"MultiEdit"` definition. `NotebookEdit` (control) appears 21 times.
6. Empirical: prompt "make these three edits ... if a MultiEdit tool is available use it" -> model answered `Edit`; tools fired: Read, Edit, Edit, Edit; three separate PreToolUse events, each with the single-edit shape:

```
{"tool_name":"Edit","tool_input":{"file_path":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/sample.txt","old_string":"alpha line one","new_string":"ALPHA 1","replace_all":false}}
{"tool_name":"Edit","tool_input":{"file_path":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/sample.txt","old_string":"gamma line three","new_string":"GAMMA 3","replace_all":false}}
{"tool_name":"Edit","tool_input":{"file_path":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/sample.txt","old_string":"delta line four","new_string":"DELTA 4","replace_all":false}}
```

Conclusion: a matcher containing `MultiEdit` is harmless (regex alternative that never matches) but misleading. Amend PRD matcher to `Edit|Write` and drop the MultiEdit mention. Several edits to one file arrive as N consecutive Edit events (each with its own PreToolUse/PostToolUse), which the pre_edit/post_edit hooks should expect.

## d. PostToolUse for Edit (VERIFIED)

Verbatim:

```
{"session_id":"ad2fdeeb-812c-487b-b6fd-bd853f8d17f0","transcript_path":"/Users/fabioaraujo/.claude/projects/-private-tmp-claude-501--Users-fabioaraujo-Developer-c2b4051e-b6e0-4ea5-b4af-94b87a3a9700-scratchpad-validation-item1/ad2fdeeb-812c-487b-b6fd-bd853f8d17f0.jsonl","cwd":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1","prompt_id":"83a13549-c692-45c5-b5e1-6a5862d76019","permission_mode":"acceptEdits","hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/sample.txt","old_string":"beta line two","new_string":"BETA LINE TWO","replace_all":false},"tool_response":{"filePath":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1/sample.txt","oldString":"beta line two","newString":"BETA LINE TWO","originalFile":"alpha line one\nbeta line two\ngamma line three\ndelta line four\n","structuredPatch":[{"oldStart":1,"oldLines":4,"newStart":1,"newLines":4,"lines":[" alpha line one","-beta line two","+BETA LINE TWO"," gamma line three"," delta line four"]}],"userModified":false,"replaceAll":false},"tool_use_id":"toolu_01KQPnYchuLFoLnTR4ND6oDL","duration_ms":1}
```

Fields: everything from PreToolUse plus `tool_response` and `duration_ms`. `tool_response` for Edit = `{filePath, oldString, newString, originalFile, structuredPatch[{oldStart,oldLines,newStart,newLines,lines[]}], userModified, replaceAll}`. Note camelCase in `tool_response` vs snake_case in `tool_input`. The post_edit hook can take the path from `tool_input.file_path` (identical across Edit/Write) and can even render the diff from `structuredPatch` without shelling out to git, though `git diff -- <file>` remains valid.

## e. Blocking hook 30s with `"timeout": 120` (VERIFIED)

Config: `{ "type": "command", "command": "<abs>/hooks/slow.sh <abs>/slow_hook.txt", "timeout": 120 }` on PreToolUse matcher `Edit|MultiEdit|Write`. `slow.sh` writes `start <epoch>`, sleeps 30, writes `done <epoch>`, exits 0.

Result: rc=0, wall 45s (result.duration_ms=39483, duration_api_ms=8351, i.e. ~30s spent in the hook). Marker file:

```
start 1790302137
done 1790302167
```

delta = 30s; `done` present -> hook not killed. Edit completed (sample.txt line 2 reverted to `beta line two`), PostToolUse fired.

Control (`proj_kill/`, same 30s hook, `"timeout": 5`): rc=0, wall 17s; marker file contains only `start 1790302136` (no `done`) -> hook killed at timeout; the Edit still ran (`k.txt` line 2 became `TWO`); the model replied "Done."; stream-json contains 0 lines mentioning "timed out". So a timed-out hook is killed silently and does not block the tool, matching the PRD's assumption.

Doc quotes (https://code.claude.com/docs/en/hooks):

- Common fields, `timeout`: "Seconds before canceling. Claude Code doesn't enforce it on a command hook you run with `async: true`. Defaults: 600 for `command`, `http`, and `mcp_tool`; 30 for `prompt`; 60 for `agent`. Claude Code lowers the `command`, `http`, and `mcp_tool` default to 30 on `UserPromptSubmit`, `PreModelSwitch`, and `PostModelSwitch`, and to 10 on `MessageDisplay`."
- Timeouts: "Apart from a command hook you run with `async: true`, Claude Code cancels a `command`, `http`, or `mcp_tool` hook that reaches its `timeout`, discarding the hook's output, so on most events a timed-out hook renders no decision."
- Working directory: "Handlers run in the current directory with Claude Code's environment. If the current directory no longer exists ... Claude Code runs command hooks from the first of these that still exists: the directory the session started in, the project root, your home directory, or the system temp directory."
- `${CLAUDE_PROJECT_DIR}`: "the project root where the session started. Claude Code also sets this variable in the environment of stdio MCP servers and plugin LSP servers."

PRD implication: default timeout is 600s for PreToolUse/PostToolUse/Stop command hooks and 30s for UserPromptSubmit. `pair hook prompt` (git diff, ~200 lines) is far under 30s. The PRD line "finish in well under the hook timeout" is correct; optionally state the numbers.

## f. UserPromptSubmit additionalContext (VERIFIED, both variants)

JSON variant (`hooks/ups_json.sh` prints `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"The secret codeword for this session is ZEBRA-4471."}}`, exit 0). Run: `env -u CLAUDECODE claude -p "What is the secret codeword you were given in context? Reply with only the codeword." --model claude-haiku-4-5-20251001 --allowedTools ""` -> answer: `ZEBRA-4471` (wall 9s, rc 0).

Plain-stdout variant (`proj_plain/`, `hooks/ups_plain.sh` echoes `The secret codeword for this session is OTTER-9932.`, exit 0) -> answer: `OTTER-9932` (wall 9s, rc 0).

UserPromptSubmit stdin shape (verbatim):

```
{"session_id":"f7cb8eb5-3300-4e0d-a11d-fad9fe0d4b48","transcript_path":"/Users/fabioaraujo/.claude/projects/-private-tmp-claude-501--Users-fabioaraujo-Developer-c2b4051e-b6e0-4ea5-b4af-94b87a3a9700-scratchpad-validation-item1/f7cb8eb5-3300-4e0d-a11d-fad9fe0d4b48.jsonl","cwd":"/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item1","prompt_id":"485bc345-1379-49b8-9445-13b48ea1fed9","permission_mode":"default","hook_event_name":"UserPromptSubmit","prompt":"What is the secret codeword you were given in context? Reply with only the codeword."}
```

Fields: `session_id, transcript_path, cwd, prompt_id, permission_mode, hook_event_name, prompt`.

Doc quotes (https://code.claude.com/docs/en/hooks):
- "Claude sees the `additionalContext` text and can incorporate it into its response or use it to inform how it processes the prompt."
- Exit code 0: "For most events, Claude Code writes stdout to the debug log and doesn't show it in the transcript. The exceptions are `UserPromptSubmit`, `UserPromptExpansion`, `SessionStart`, and `PostModelSwitch`, where Claude Code adds plain-text stdout as context that Claude can see and act on."
- Exit code 2 on UserPromptSubmit: "Blocks prompt processing and erases the prompt" (so `pair hook prompt` must never exit 2).
- JSON example on the page: `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"...","permissionDecision":"deny","permissionDecisionReason":"..."}}`.

Either mechanism works; the JSON form is safer (plain stdout is also shown in the transcript and could be confused with a decision on other events).

## g. cwd in PreToolUse stdin (VERIFIED)

`cwd` present in every event dumped (PreToolUse, PostToolUse, UserPromptSubmit) and equals the project dir. Hook process `$PWD` == `cwd` == `$CLAUDE_PROJECT_DIR`. `tool_input.file_path` was absolute in all observed Edit/Write calls, but the hook should still resolve relative paths against `cwd`.

## Other observations

- The user's global settings include a Warp plugin `SessionStart` hook; it fires in nested sessions and appears in stream-json as `system/hook_started` / `system/hook_response`. PreToolUse/PostToolUse hook outcomes are NOT surfaced as stream events, so hook liveness has to be checked via side effects (as done here).
- Nested `claude -p` prints "Warning: no stdin data received in 3s" when stdin is a terminal-less pipe; pass `< /dev/null` to silence.
- All settings.json files, hook scripts, raw stream logs and evidence_*.jsonl files are kept in this directory.

## Files

- `.claude/settings.json`, `proj_plain/.claude/settings.json`, `proj_kill/.claude/settings.json`
- `hooks/dump.sh`, `hooks/slow.sh`, `hooks/ups_json.sh`, `hooks/ups_plain.sh`
- `evidence_edit_pre.jsonl`, `evidence_edit_post.jsonl`, `evidence_write_pre.jsonl`, `evidence_write_post.jsonl`, `evidence_multi_pre.jsonl`, `evidence_multi_post.jsonl`, `hook_ups_stdin.jsonl`, `slow_hook.txt`, `proj_kill/slow_hook.txt`
- `run_*.stream.jsonl`, `run_*.out`, `run_*.time`, `run_*.stderr`
