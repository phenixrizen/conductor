<script setup lang="ts">
import type { TransportKind } from '~/utils/protocol'
import type { TransportState } from '~/utils/transport/types'

const props = defineProps<{ kind: TransportKind; state: TransportState }>()

const label = computed(() => {
  switch (props.state) {
    case 'connecting':
      return 'connecting'
    case 'signaling':
      return 'negotiating WebRTC'
    case 'closed':
      return 'disconnected'
    case 'idle':
      return 'idle'
  }
  switch (props.kind) {
    case 'ws':
      return 'websocket'
    case 'webrtc':
      return 'webrtc p2p'
    case 'relay':
      return 'relay'
  }
  return props.kind
})

const color = computed(() => {
  if (props.state === 'closed') return 'error'
  if (props.state !== 'open') return 'warning'
  return props.kind === 'webrtc' ? 'primary' : 'neutral'
})

const icon = computed(() => {
  if (props.state !== 'open') return 'i-lucide-loader-circle'
  return props.kind === 'webrtc' ? 'i-lucide-radio' : props.kind === 'relay' ? 'i-lucide-route' : 'i-lucide-cable'
})
</script>

<template>
  <UBadge :label="label" :icon="icon" :color="color" variant="outline" size="sm" />
</template>
