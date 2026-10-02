#!/usr/bin/env bash
# stub-agent.sh stands in for Claude Code and Codex in the e2e suite
# (web/e2e/conductor.e2e.json gives the claude and codex ids this script).
# It never runs what it reads. Each line typed into it (a role prompt, a
# handoff, a broadcast, a person's words) is echoed; in it, only the merges
# the example crews ask for are recognised, by fixed patterns, for branches
# of this run alone (crew/<run>/<member>, both ids checked), and run as git's
# argv. Then it commits a file of its own on its branch and reports done
# through conductor notify. It turns bracketed paste on, as the agents do, and
# takes the paste markers off what it reads.
set -euo pipefail

member=${CONDUCTOR_MEMBER:-agent}
run=${CONDUCTOR_RUN:-}
conductor=${CONDUCTOR_BIN:-conductor}
wait_s=${STUB_MERGE_WAIT_S:-60}

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

# report sends a report to Conductor; outside a session it does nothing.
report() { "$conductor" notify "$@" >/dev/null 2>&1 || true; }

# holds reports whether branch $1 has stub-$2.txt, the file member $2 commits.
holds() { git cat-file -e "$1:stub-$2.txt" 2>/dev/null; }

# answer answers one line.
answer() {
	local line=$1 rest=$1 kind target_run target branch deadline top to
	local -a merged=() failed=()
	printf 'got: %s\n' "$line"
	report --state working
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
	report --state done --message "got: ${line:0:100}; merged: ${merged[*]-none}; failed: ${failed[*]-none}"
}

printf 'conductor e2e stub: %s in run %s\n' "$member" "${run:-none}"
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
