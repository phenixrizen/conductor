<script setup lang="ts">
import { copyrightLine, CREDIT, LICENSE, LICENSE_URL, SOURCE_URL, SPONSOR_HOST, SPONSOR_URL, versionOn } from '~/utils/about'

/**
 * Settings → The app (the Sponsor Kit's 1c): what runs, where its source is, the copyright, and the sponsor credit as the
 * card's last row and nowhere else in the app. The desktop app passes its own versions; in a browser the server's are shown.
 */
const props = defineProps<{ versions?: { app: string; server: string; electron: string; chrome: string } | null }>()
const serverHost = useServerHost()
serverHost.load()

const rows = computed(() => {
  const v = props.versions
  const server = versionOn(v?.server || serverHost.version.value, serverHost.host.value)
  const out: Array<{ key: string; label: string; value: string; mono?: boolean }> = []
  if (v?.app) out.push({ key: 'app', label: 'Conductor', value: v.app, mono: true })
  if (server) out.push({ key: 'server', label: 'Server', value: server, mono: true })
  if (v?.electron) out.push({ key: 'runtime', label: 'Runtime', value: `electron ${v.electron} · chrome ${v.chrome}`, mono: true })
  return out
})
</script>

<template>
  <UCard data-about-card>
    <template #header><h2 class="font-semibold">The app</h2></template>
    <div class="flex flex-col gap-4">
      <slot />
      <dl class="grid grid-cols-[7.5rem_minmax(0,1fr)] gap-x-4 gap-y-2.5 text-sm">
        <template v-for="r in rows" :key="r.key">
          <dt class="text-muted">{{ r.label }}</dt>
          <dd class="min-w-0 truncate text-highlighted" :class="{ 'font-mono': r.mono }" :data-about-row="r.key">{{ r.value }}</dd>
        </template>
        <dt class="text-muted">License</dt>
        <dd data-about-license><ULink :to="LICENSE_URL" target="_blank" class="text-primary">{{ LICENSE }}</ULink> · <ULink :to="SOURCE_URL" target="_blank" class="text-primary">Source</ULink></dd>
        <dt class="text-muted">Copyright</dt>
        <dd data-about-copyright>{{ copyrightLine() }}</dd>
      </dl>
      <div class="flex items-center gap-3 border-t border-default pt-3.5" data-about-credit>
        <span class="flex size-8 flex-none items-center justify-center rounded-md bg-elevated ring ring-default">
          <img src="/sponsor/rocksolidlabs-mark.png" alt="" class="size-5" />
        </span>
        <div class="flex min-w-0 flex-col gap-0.5">
          <span class="text-sm font-medium text-highlighted">{{ CREDIT }}</span>
          <ULink :to="SPONSOR_URL" target="_blank" class="text-xs text-muted hover:text-default">{{ SPONSOR_HOST }}</ULink>
        </div>
      </div>
    </div>
  </UCard>
</template>
