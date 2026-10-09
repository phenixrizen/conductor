#!/usr/bin/env bash
# stub-agent.sh stands in for Claude Code, Codex and Goose in the e2e suite
# (web/e2e/conductor.e2e.json gives those ids this script, with the real
# agents' recipes). It never runs what it reads. Each line typed into it (a
# role prompt, a handoff, a broadcast, a person's words) is echoed; in it,
# only the merges the example crews ask for are recognised, by fixed
# patterns, for branches of this run alone (crew/<run>/<member>, both ids
# checked), and run as git's argv. Then it commits a file of its own on its
# branch and reports done the way its agent does. It turns bracketed paste
# on, as the agents do, and takes the paste markers off what it reads.
#
# What the environment chooses:
#   STUB_IDENTITY   claude | codex | pressly-goose | stub: what --version prints
#   STUB_REPORT     plain | claude | codex: notify's words, or the agent's
#                   hook payloads (Claude Code's UserPromptSubmit and Stop,
#                   Codex's agent-turn-complete with the thread id)
#   STUB_CODEX_TITLE=1   a Codex title-thread turn is reported before the
#                   answer, with a higher thread id, as Codex 0.159 does
#   STUB_TRUST_DIALOG=1  "Trust this folder?" is drawn first and answered
#                   by Enter alone, which writes the trust into
#                   $HOME/.codex/config.toml; a launch with -c projects=…
#                   (the yolo trust override) asks nothing
#   STUB_CHOICES    a|b|c: the stub asks STUB_ASK (else "Which one?") with
#                   these as choices (notify --choices) before its prompt;
#                   the next line typed answers it
# $HOME/.codex/stub-hooks-review present: "Hooks need review" is drawn at
# start as Codex 0.161 draws it when its hooks are new or changed (round 13,
# G2c): 1. Review hooks highlighted; a digit moves the highlight, Enter
# picks it; 2 trusts ("Hooks trusted."), 3 goes on without ("Hooks off."),
# 1 opens the review ("Reviewing hooks."), which a prompt's Enter would;
# the choice is written to $HOME/.codex/stub-hooks-answer.
# A line that is exactly `stub tools codex`, `stub tools copilot`,
# `stub tools agy` or `stub tools goose` makes the stub write stub-tools.txt and report, through
# that agent's hook flag, the payloads a live run of it sends (round 13,
# G2b): a shell `cat README.md` and the write of stub-tools.txt, in the
# shapes captured in internal/notify/testdata.
# The agent's session: --session-id ID names it, --resume ID (or a leading
# `resume ID`) continues it; without either a fresh id is made. The
# transcript lives in $HOME/.stub-sessions/<id>.txt, so a resumed session
# prints what was typed into it before.
set -euo pipefail

identity=${STUB_IDENTITY:-stub}
report_mode=${STUB_REPORT:-plain}
member=${CONDUCTOR_MEMBER:-agent}
run=${CONDUCTOR_RUN:-}
conductor=${CONDUCTOR_BIN:-conductor}
wait_s=${STUB_MERGE_WAIT_S:-60}

case "${1:-}" in
--version | -V)
	case $identity in
	claude) echo "2.1.287 (Claude Code)" ;;
	codex) echo "codex-cli 0.159.0" ;;
	pressly-goose) echo "goose version: v3.22.1" ;;
	*) echo "conductor e2e stub 2.0" ;;
	esac
	exit 0
	;;
--help | -h)
	cat <<'HELP'
Usage: stub-agent.sh [options] [resume ID]

Options:
  --session-id ID    name the agent's own session
  --resume ID        continue a session
  -c KEY=VALUE       a configuration override (projects=... trusts the folder)
  --settings FILE    extra settings (ignored)
  --dangerously-skip-permissions
  --dangerously-bypass-approvals-and-sandbox
  --version          print the version
HELP
	exit 0
	;;
esac

id_re='^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'
run_re='^[a-z0-9][a-z0-9-]{0,40}-[0-9a-f]{8}$'
member_re='^[a-z0-9][a-z0-9._-]{0,39}$'
target_re='(git merge --no-edit|wait until) crew/(\$CONDUCTOR_RUN|[a-z0-9][a-z0-9-]*)/([a-z0-9][a-z0-9._-]*)'
handoff_re='--event handoff --to ([a-z0-9][a-z0-9._-]{0,39})'
paste_start=$'\e[200~'
paste_end=$'\e[201~'
git_id=(-c user.name=conductor-e2e -c user.email=e2e@conductor.invalid -c commit.gpgsign=false)

[[ $member =~ $member_re ]] || member=agent
[[ $run =~ $run_re ]] || run=
handed_off=

# The arguments: the session, the trust override, the yolo flags. Anything
# else is taken and ignored, as a flag of the real agent would be.
session_id= resume_id= trusted= yolo=
args=("$@")
i=0
while ((i < ${#args[@]})); do
	a=${args[i]}
	case $a in
	--session-id)
		session_id=${args[i + 1]:-}
		i=$((i + 2))
		continue
		;;
	--resume)
		resume_id=${args[i + 1]:-}
		i=$((i + 2))
		continue
		;;
	resume)
		if ((i == 0)); then
			resume_id=${args[1]:-}
			i=2
			continue
		fi
		;;
	-c)
		[[ ${args[i + 1]:-} == projects=* ]] && trusted=1
		i=$((i + 2))
		continue
		;;
	--settings)
		i=$((i + 2))
		continue
		;;
	--dangerously-skip-permissions | --dangerously-bypass-approvals-and-sandbox | --yolo) yolo=1 ;;
	esac
	i=$((i + 1))
done
[[ -n $session_id && ! $session_id =~ $id_re ]] && session_id=
[[ -n $resume_id && ! $resume_id =~ $id_re ]] && resume_id=

newid() {
	if [[ -r /proc/sys/kernel/random/uuid ]]; then
		tr 'A-F' 'a-f' </proc/sys/kernel/random/uuid
	elif command -v uuidgen >/dev/null 2>&1; then
		uuidgen | tr 'A-F' 'a-f'
	else
		printf '%08x-%04x-4%03x-8%03x-%012x\n' "$RANDOM$RANDOM" "$RANDOM" "$((RANDOM % 4096))" "$((RANDOM % 4096))" "$(date +%s)$RANDOM"
	fi
}
id=${resume_id:-${session_id:-$(newid)}}
# A thread id above the session's for Codex's title thread (lowest wins).
title_id="ffffffff-${id:9}"
[[ $title_id =~ $id_re ]] || title_id=ffffffff-ffff-ffff-ffff-ffffffffffff

transcripts=${HOME:-/tmp}/.stub-sessions
mkdir -p "$transcripts"
transcript=$transcripts/$id.txt

# json_str prints $1 as a JSON string.
json_str() {
	local s=$1
	s=$(printf '%s' "$s" | tr -d '\000-\010\013\014\016-\037')
	s=${s//\\/\\\\}
	s=${s//\"/\\\"}
	s=${s//$'\n'/\\n}
	s=${s//$'\t'/\\t}
	printf '"%s"' "$s"
}

# report sends a plain report to Conductor; outside a session it does nothing.
report() { "$conductor" notify "$@" >/dev/null 2>&1 || true; }
# claude_hook sends a Claude Code hook payload.
claude_hook() { printf '%s' "$1" | "$conductor" notify --claude-hook >/dev/null 2>&1 || true; }
# codex_notify sends a Codex notify payload for thread $1 with the answer $2 and the input $3.
codex_notify() { "$conductor" notify --codex "{\"type\":\"agent-turn-complete\",\"thread-id\":\"$1\",\"last-assistant-message\":$(json_str "$2"),\"input-messages\":[$(json_str "$3")]}" >/dev/null 2>&1 || true; }

report_working() {
	case $report_mode in
	claude) claude_hook "{\"hook_event_name\":\"UserPromptSubmit\",\"session_id\":\"$id\"}" ;;
	codex) : ;;
	*) report --state working ;;
	esac
}

# tool_replay writes stub-tools.txt and reports agent $1's hook payloads
# for a shell read of README.md and that write, as a live run sends them.
tool_replay() {
	local dir write read
	dir=$(json_str "$PWD")
	printf 'from the stub\n' >stub-tools.txt
	case $1 in
	codex)
		read="{\"hook_event_name\":\"PostToolUse\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"cat README.md\"},\"cwd\":$dir}"
		write="{\"hook_event_name\":\"PostToolUse\",\"tool_name\":\"apply_patch\",\"tool_input\":{\"command\":$(json_str "*** Begin Patch
*** Add File: $PWD/stub-tools.txt
+from the stub
*** End Patch")},\"cwd\":$dir}"
		;;
	copilot)
		read="{\"toolName\":\"bash\",\"toolArgs\":{\"command\":\"cat README.md\",\"description\":\"Show README\"},\"toolResult\":{\"resultType\":\"success\",\"textResultForLlm\":\"\"},\"cwd\":$dir}"
		write="{\"toolName\":\"create\",\"toolArgs\":{\"path\":$(json_str "$PWD/stub-tools.txt"),\"file_text\":\"from the stub\"},\"toolResult\":{\"resultType\":\"success\",\"textResultForLlm\":\"\"},\"cwd\":$dir}"
		;;
	goose)
		read="{\"event\":\"PostToolUse\",\"tool_name\":\"shell\",\"tool_input\":{\"command\":\"cat README.md\"},\"working_dir\":$dir}"
		write="{\"event\":\"PostToolUse\",\"tool_name\":\"write\",\"tool_input\":{\"path\":\"stub-tools.txt\",\"content\":\"from the stub\"},\"working_dir\":$dir}"
		;;
	agy)
		read="{\"toolCall\":{\"name\":\"run_command\",\"args\":{\"CommandLine\":\"cat README.md\",\"Cwd\":$dir}},\"error\":\"\"}"
		write="{\"toolCall\":{\"name\":\"write_to_file\",\"args\":{\"TargetFile\":$(json_str "$PWD/stub-tools.txt"),\"CodeContent\":\"from the stub\"}},\"error\":\"\"}"
		;;
	esac
	printf '%s' "$read" | "$conductor" notify "--$1-hook" >/dev/null 2>&1 || true
	printf '%s' "$write" | "$conductor" notify "--$1-hook" >/dev/null 2>&1 || true
}

# report_done reports the answer $1 to the line $2.
report_done() {
	case $report_mode in
	claude) claude_hook "{\"hook_event_name\":\"Stop\",\"session_id\":\"$id\",\"last_assistant_message\":$(json_str "$1")}" ;;
	codex)
		if [[ ${STUB_CODEX_TITLE:-} == 1 ]]; then
			codex_notify "$title_id" "Fix the todo app" "Generate a concise, single-line task title for the following conversation: $2"
		fi
		codex_notify "$id" "$1" "$2"
		;;
	*) report --state done --message "$1" ;;
	esac
}

# holds reports whether branch $1 has stub-$2.txt, the file member $2 commits.
holds() { git cat-file -e "$1:stub-$2.txt" 2>/dev/null; }

# trust_dialog draws the folder-trust question and waits for Enter; arrows
# move the selection and redraw, anything else is ignored. Enter writes the
# trust the way Codex keeps it.
trust_dialog() {
	local sel=0 key seq
	draw() {
		local y n
		if ((sel == 0)); then y='>' n=' '; else y=' ' n='>'; fi
		printf '\r\e[2K\e[1A\e[2K\e[1A\e[2KTrust this folder? (Enter accepts, arrows move)\n  %s Yes, trust this folder\n  %s No, continue without\n' "$y" "$n"
	}
	printf 'Trust this folder? (Enter accepts, arrows move)\n  > Yes, trust this folder\n    No, continue without\n'
	while IFS= read -rsn1 key; do
		if [[ -z $key || $key == $'\r' ]]; then
			break
		fi
		if [[ $key == $'\e' ]]; then
			seq=''
			IFS= read -rsn2 -t 0.2 seq || true
			case $seq in
			'[A' | '[B') sel=$((1 - sel)) ;;
			esac
			draw
		fi
	done
	if ((sel == 0)); then
		mkdir -p "${HOME:-/tmp}/.codex"
		printf '[projects.%s]\ntrust_level = "trusted"\n' "$(json_str "$PWD")" >>"${HOME:-/tmp}/.codex/config.toml"
		printf 'Trusted.\n'
	else
		printf 'Not trusted.\n'
	fi
}

# hooks_review draws Codex's "Hooks need review" and waits for Enter; a
# digit moves the highlight.
hooks_review() {
	local sel=1 key
	printf 'Hooks need review\n7 hooks are new or changed.\nHooks can run outside the sandbox after you trust them.\n> 1. Review hooks\n  2. Trust all and continue\n  3. Continue without trusting (hooks won'"'"'t run)\n'
	while IFS= read -rsn1 key; do
		if [[ -z $key || $key == $'\r' ]]; then
			break
		fi
		case $key in
		1 | 2 | 3) sel=$key ;;
		esac
	done
	printf '%s\n' "$sel" >"${HOME:-/tmp}/.codex/stub-hooks-answer"
	case $sel in
	2) printf 'Hooks trusted.\n' ;;
	3) printf 'Hooks off.\n' ;;
	*) printf 'Reviewing hooks.\n' ;;
	esac
}

# answer answers one line.
answer() {
	local line=$1 rest=$1 kind target_run target branch deadline top to msg
	local -a merged=() failed=()
	printf 'got: %s\n' "$line"
	printf '%s\n' "${line:0:200}" >>"$transcript"
	report_working
	if [[ $line =~ ^stub\ tools\ (codex|copilot|agy|goose)$ ]]; then
		tool_replay "${BASH_REMATCH[1]}"
	fi
	while [[ $rest =~ $target_re ]]; do
		kind=${BASH_REMATCH[1]}
		target_run=${BASH_REMATCH[2]}
		target=${BASH_REMATCH[3]}
		rest=${rest#*"${BASH_REMATCH[0]}"}
		while [[ $target == *. ]]; do target=${target%.}; done
		[[ $target_run == '$CONDUCTOR_RUN' ]] && target_run=$run
		if [[ -z $run || $target_run != "$run" || ! $target =~ $member_re ]]; then
			printf 'stub: crew/%s/%s is not a branch of this run: left alone\n' "$target_run" "$target"
			continue
		fi
		branch=crew/$run/$target
		if [[ $kind == 'wait until' ]]; then
			deadline=$((SECONDS + wait_s))
			until holds "$branch" "$target"; do
				if ((SECONDS >= deadline)); then
					printf 'stub: %s did not hold stub-%s.txt within %ss\n' "$branch" "$target" "$wait_s"
					break
				fi
				sleep 0.5
			done
			continue
		fi
		if git "${git_id[@]}" merge -q --no-edit -- "$branch" >/dev/null 2>&1; then
			merged+=("$target")
		else
			git merge --abort >/dev/null 2>&1 || true
			failed+=("$target")
		fi
	done
	top=$(git rev-parse --show-toplevel 2>/dev/null) || top=.
	printf '%s\n' "${line:0:200}" >>"$top/stub-$member.txt"
	git -C "$top" add -- "stub-$member.txt" >/dev/null 2>&1 || true
	git -C "$top" "${git_id[@]}" commit -q -m "stub: $member answers" >/dev/null 2>&1 || true
	if [[ -z $handed_off && $line =~ $handoff_re ]]; then
		to=${BASH_REMATCH[1]}
		if [[ $to != "$member" ]]; then
			handed_off=1
			report --event handoff --to "$to" --message "stub $member: over to $to"
		fi
	fi
	printf 'stub: merged [%s] failed [%s]\n' "${merged[*]-}" "${failed[*]-}"
	msg="got: ${line:0:100}; merged: ${merged[*]-none}; failed: ${failed[*]-none}"
	report_done "$msg" "$line"
}

printf 'conductor e2e stub: %s in run %s (%s, session %s%s%s)\n' "$member" "${run:-none}" "$identity" "$id" "${yolo:+, yolo}" "${trusted:+, trusted}"
if [[ -n $resume_id ]]; then
	if [[ -s $transcript ]]; then
		printf 'stub: resumed %s: %s lines\n' "$id" "$(wc -l <"$transcript" | tr -d ' ')"
		cat "$transcript"
	else
		printf 'stub: resumed %s: 0 lines\n' "$id"
	fi
else
	: >>"$transcript"
fi
if [[ ${STUB_TRUST_DIALOG:-} == 1 && -z $trusted ]]; then
	trust_dialog
fi
if [[ -e ${HOME:-/nonexistent}/.codex/stub-hooks-review ]]; then
	hooks_review
fi
if [[ -n ${STUB_CHOICES:-} ]]; then
	printf '%s [%s]\n' "${STUB_ASK:-Which one?}" "$STUB_CHOICES"
	report --state needs_input --message "${STUB_ASK:-Which one?}" --choices "$STUB_CHOICES"
fi
printf '\e[?2004h> '
while IFS= read -r line; do
	line=${line//"$paste_start"/}
	line=${line//"$paste_end"/}
	line=${line%$'\r'}
	if [[ -n ${line//[[:space:]]/} ]]; then
		answer "$line"
	fi
	printf '> '
done
