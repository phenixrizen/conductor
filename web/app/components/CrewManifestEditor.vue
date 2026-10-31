<script setup lang="ts">
import type { CrewCheck, CrewTask } from '~/composables/useSessions'
import { TASK_LIMITS, manifestEmpty, manifestMarks, type DraftCheck, type DraftManifest, type DraftProbe, type DraftTask, type ManifestList } from '~/utils/crews'

/**
 * A managed crew's project manifest (EN1, docs/plans/project-manifests.md): what makes a fresh tree of the project usable (an attempt's
 * worktree, the checkout of its result), how a result is checked beyond its task's checks and how a tree is cleaned up; the crew's own, or
 * conductor.manifest.json read from the repository at each tree's commit. Not an agent's recipes, which are the catalog's. Every rule the
 * server holds a manifest to is marked where it is broken (manifestMarks); the page's Save and Launch read the same rules (taskProblems);
 * the server still decides.
 */
const manifest = defineModel<DraftManifest | undefined>()
const props = defineProps<{ tasks: ReadonlyArray<CrewTask | DraftTask>; setup?: readonly CrewCheck[] }>()

const m = computed<DraftManifest>(() => manifest.value ?? {})
const marks = computed(() => manifestMarks({ manifest: manifest.value, setup: props.setup, tasks: props.tasks }))
const fromFile = computed(() => !!m.value.file)

/** Sets the manifest: one that holds nothing is none, as the server keeps it. */
function put(next: DraftManifest) {
  manifest.value = manifestEmpty(next) ? undefined : next
}

function setSteps(list: Exclude<ManifestList, 'ready'>, steps: DraftCheck[]) {
  put({ ...m.value, [list]: steps.length ? steps : undefined })
}

function setProbes(probes: DraftProbe[]) {
  put({ ...m.value, ready: probes.length ? probes : undefined })
}

function setNames(key: 'env' | 'ports', names: unknown[]) {
  const next = names.map(String)
  put({ ...m.value, [key]: next.length ? next : undefined })
}

function setPreview(text: string) {
  put({ ...m.value, preview: text || undefined })
}

// The lists, while the file switch is on, are parked in the draft's manifest (never sent): turning it off again gives them back, after a visit
// to the Runs tab or a save too.
function setFile(on: boolean) {
  const { file: _file, parked, ...rest } = m.value
  if (on) {
    put(manifestEmpty(rest) ? { file: true } : { file: true, parked: rest })
    return
  }
  put(manifestEmpty(rest) ? (parked ?? {}) : rest)
}

/** A space or a comma ends a name typed into the env and ports chips, as Enter does. */
const NAME_DELIMITER = /[\s,]/

const LISTS: ManifestList[] = ['prerequisites', 'setup', 'services', 'ready', 'verify', 'cleanup']
const checkIds = computed(() => props.tasks.flatMap((t) => (t.checks ?? []).map((c) => c.id)))
/** The ids a new row of `list` may not take: every other list's, and every task's checks'. */
function takenBesides(list: ManifestList): string[] {
  const out = [...checkIds.value]
  for (const name of LISTS) if (name !== list) for (const row of m.value[name] ?? []) out.push(row.id)
  return out
}
</script>

<template>
  <div class="flex flex-col gap-3" data-crew-manifest>
    <div class="flex flex-wrap items-baseline gap-x-2.5 gap-y-1.5">
      <span class="text-sm font-semibold text-highlighted">Project manifest</span>
      <span class="min-w-0 text-[12.5px] text-muted">what makes a fresh worktree of this project usable, what checks a result beyond its task's checks, and what cleans up after; not an agent recipe, which the catalog keeps for launching an agent</span>
    </div>
    <div class="flex flex-col gap-4 rounded-lg p-3 ring ring-default" data-crew-manifest-body>
      <USwitch :model-value="fromFile" size="sm" data-manifest-file @update:model-value="setFile">
        <template #label>Read <code class="font-mono">conductor.manifest.json</code> from the repository at each attempt's commit</template>
      </USwitch>
      <p v-for="(p, i) in marks.manifest" :key="i" class="text-xs leading-snug text-error" data-manifest-error>{{ p }}</p>
      <p v-if="fromFile" class="text-xs text-muted" data-manifest-file-note>
        An attempt's worktree reads the file at the commit it starts from, and the checkout of its result at the result, so a task that changes the
        file is checked by the file it changed. It holds what the lists here would, with <code class="font-mono">"schema": 1</code>; a broken file
        refuses the launch, and a commit without it fails its tree.
      </p>
      <template v-else>
        <p class="-mt-1 text-xs text-muted">
          Every step is a program and its arguments, never a shell line, run as you on this machine in a tree: an attempt's worktree, or the
          checkout of its result. In an argument, a probe's URL and the preview, <code class="font-mono">${CONDUCTOR_PORT_&lt;NAME&gt;}</code> is the
          tree's own port and <code class="font-mono">${CONDUCTOR_STATE_DIR}</code> its own directory for state.
        </p>

        <section class="flex flex-col gap-1.5" data-manifest-phase="prerequisites">
          <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span class="text-[13px] font-medium text-highlighted">Prerequisites</span>
            <span class="text-xs text-muted">once per run, in the crew's directory before any worktree: what this machine must have. A failing one pauses the run.</span>
          </div>
          <CrewCheckList :model-value="m.prerequisites ?? []" what="prerequisite" id-base="prerequisite" :max="TASK_LIMITS.manifestSteps" :problems="marks.rows.prerequisites" :taken="takenBesides('prerequisites')" :timeout-default="TASK_LIMITS.prerequisiteTimeoutDefault" @update:model-value="setSteps('prerequisites', $event)" />
        </section>

        <div class="grid gap-3 md:grid-cols-2">
          <UFormField label="Env" name="manifest-env" :error="marks.env || undefined" :ui="{ help: 'mt-1 text-xs', error: 'mt-1 text-xs' }" help="the host's variables the steps and the agent need, by name; the server's envPassthrough decides which leave it" data-manifest-field="env">
            <UInputTags :model-value="m.env ?? []" :max="TASK_LIMITS.manifestEnv" :max-length="128" :delimiter="NAME_DELIMITER" add-on-blur placeholder="NPM_TOKEN" aria-label="Env names" :ui="{ input: 'font-mono', itemText: 'font-mono' }" class="w-full" @update:model-value="setNames('env', $event)" />
          </UFormField>
          <UFormField label="Ports" name="manifest-ports" :error="marks.ports || undefined" :ui="{ help: 'mt-1 text-xs', error: 'mt-1 text-xs' }" help="a free port of its own per name in each tree, as ${CONDUCTOR_PORT_<NAME>} in upper case" data-manifest-field="ports">
            <UInputTags :model-value="m.ports ?? []" :max="TASK_LIMITS.manifestPorts" :max-length="16" :delimiter="NAME_DELIMITER" add-on-blur placeholder="web" aria-label="Port names" :ui="{ input: 'font-mono', itemText: 'font-mono' }" class="w-full" @update:model-value="setNames('ports', $event)" />
          </UFormField>
        </div>

        <section class="flex flex-col gap-1.5" data-manifest-phase="setup">
          <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span class="text-[13px] font-medium text-highlighted">Setup</span>
            <span class="text-xs text-muted">in every new worktree before its member starts, and in the checkout of a result before its checks: what a fresh tree lacks (a dependency install)</span>
          </div>
          <CrewCheckList :model-value="m.setup ?? []" what="setup step" id-base="setup" :max="TASK_LIMITS.manifestSteps" :problems="marks.rows.setup" :taken="takenBesides('setup')" :timeout-default="TASK_LIMITS.timeoutDefault" @update:model-value="setSteps('setup', $event)" />
        </section>

        <section class="flex flex-col gap-1.5" data-manifest-phase="services">
          <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span class="text-[13px] font-medium text-highlighted">Services</span>
            <span class="text-xs text-muted">started after setup and kept running beside the tree as long as it lives, in the foreground: a database, a dev server</span>
          </div>
          <CrewCheckList :model-value="m.services ?? []" what="service" id-base="service" :max="TASK_LIMITS.manifestServices" :problems="marks.rows.services" :taken="takenBesides('services')" no-timeout @update:model-value="setSteps('services', $event)" />
        </section>

        <section class="flex flex-col gap-1.5" data-manifest-phase="ready">
          <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span class="text-[13px] font-medium text-highlighted">Ready</span>
            <span class="text-xs text-muted">tried until the services answer, before the member starts or the checks run: a URL on this machine until it answers, or a command until it exits 0</span>
          </div>
          <CrewProbeList :model-value="m.ready ?? []" :problems="marks.rows.ready" :taken="takenBesides('ready')" @update:model-value="setProbes" />
        </section>

        <UFormField label="Preview" name="manifest-preview" :error="marks.preview || undefined" :ui="{ help: 'mt-1 text-xs', error: 'mt-1 text-xs' }" help="the URL to open while an attempt runs: on this machine, at a port of the manifest's" data-manifest-field="preview">
          <UInput :model-value="m.preview ?? ''" placeholder="http://127.0.0.1:${CONDUCTOR_PORT_WEB}/" autocapitalize="off" spellcheck="false" aria-label="Preview URL" :ui="{ base: 'font-mono' }" class="w-full" @update:model-value="setPreview(String($event))" />
        </UFormField>

        <section class="flex flex-col gap-1.5" data-manifest-phase="verify">
          <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span class="text-[13px] font-medium text-highlighted">Verify</span>
            <span class="text-xs text-muted">after the task's checks, in the checkout of a result: each must pass, as a check must</span>
          </div>
          <CrewCheckList :model-value="m.verify ?? []" what="verify step" id-base="verify" :max="TASK_LIMITS.manifestSteps" :problems="marks.rows.verify" :taken="takenBesides('verify')" :timeout-default="TASK_LIMITS.timeoutDefault" @update:model-value="setSteps('verify', $event)" />
        </section>

        <section class="flex flex-col gap-1.5" data-manifest-phase="cleanup">
          <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span class="text-[13px] font-medium text-highlighted">Cleanup</span>
            <span class="text-xs text-muted">once a tree ends, however it ends (a result taken, a failure, a cancel, a stop), after its services are stopped</span>
          </div>
          <CrewCheckList :model-value="m.cleanup ?? []" what="cleanup step" id-base="cleanup" :max="TASK_LIMITS.manifestSteps" :problems="marks.rows.cleanup" :taken="takenBesides('cleanup')" :timeout-default="TASK_LIMITS.cleanupTimeoutDefault" @update:model-value="setSteps('cleanup', $event)" />
        </section>
      </template>
    </div>
  </div>
</template>
