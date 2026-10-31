<script setup lang="ts">
import type { CommentReply, DispatchTicket, TicketComment } from '~/composables/useDispatch'
import type { DispatchOrder } from '~/utils/dispatchOrder'
import { through } from '~/utils/dispatchOrder'
import { COMMENT_PAGE, commentAuthor, earlierComments, shownComments } from '~/utils/dispatch'
import { avatarTone } from '~/utils/avatar'
import { initials, relativeTime } from '~/utils/sessions'

/**
 * A ticket's discussion (docs/plans/dispatch.md, PR-10): its comments in their order, each with its number (`#3`, the store's, derived
 * from its place, so a deleted comment keeps its number and none moves), who wrote it and when, its body as Markdown with the ticket's
 * images; edit and delete (a tombstone) on a live ticket; the last 50 as the ticket's read carries them and the earlier ones a page at a
 * time; a new comment at the foot. Each is one operation (useDispatchOps); its answer is shown at once, the ticket's re-read on its
 * event (another browser's comment among them) bringing the rest. The earlier pages shown are read again whenever the page reads the
 * ticket again (`reads` moving), so an old comment edited elsewhere is shown as it is now.
 */
const props = defineProps<{
  ticket: DispatchTicket
  gate: DispatchOrder
  locked: boolean
  /** Counts the page's reads of the ticket: each one reads the earlier pages shown again. */
  reads: number
  attachmentUrl?: (id: string) => Promise<string | null>
  now: number
}>()

const api = useDispatchApi()
const ops = useDispatchOps(props.gate)
const toast = useToast()

/** The pages before the ticket's read's, as read. */
const earlier = ref<TicketComment[]>([])
/** The comments this page's operations answered with, by id: newer than a read sent before them and served after. */
const known = ref(new Map<string, TicketComment>())
const loadingEarlier = ref(false)
const editing = ref('')
const confirming = ref('')

const comments = computed(() => shownComments(props.ticket.id, props.ticket.comments ?? [], earlier.value, known.value))
const before = computed(() => earlierComments(comments.value[0]?.n))

// Another ticket on the same page: nothing of this one's stays.
watch(
  () => props.ticket.id,
  () => {
    earlier.value = []
    known.value = new Map()
    editing.value = ''
    confirming.value = ''
  },
)

/**
 * Reads again the earlier pages shown, those numbered `from` up to the first the ticket's read carries, 50 at a time, as one answer
 * through the page's gate, so a slower read never replaces a later one.
 */
async function readEarlier(from: number): Promise<void> {
  const key = props.ticket.key
  const id = props.ticket.id
  const to = (props.ticket.comments?.[0]?.n ?? Number.MAX_SAFE_INTEGER) - 1
  if (to < from) return
  await through(
    props.gate,
    `comments:${key}`,
    async () => {
      const all: TicketComment[] = []
      for (let after = from - 1; after < to; after += COMMENT_PAGE) {
        const page = await api.comments(key, after, Math.min(COMMENT_PAGE, to - after))
        all.push(...page)
        if (page.length < Math.min(COMMENT_PAGE, to - after)) break
      }
      return all
    },
    (all) => {
      if (props.ticket.id === id) earlier.value = all
    },
  )
}

/** The page before those shown: the earlier pages read again with it, from its first, as one answer. */
async function more() {
  const page = earlierComments(comments.value[0]?.n)
  if (!page) return
  loadingEarlier.value = true
  try {
    await readEarlier(page.after + 1)
  } catch (e) {
    toast.add({ title: 'The earlier comments were not read', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    loadingEarlier.value = false
  }
}

// The page read the ticket again: the earlier pages shown are read again too, from the first of them.
watch(
  () => props.reads,
  () => {
    const first = earlier.value[0]?.n
    if (first) void readEarlier(first).catch(() => {})
  },
)

/** A comment an operation answered with: shown at once, at its revision or a later one. */
function keep(c: TicketComment | null | undefined) {
  if (!c || c.ticketId !== props.ticket.id) return
  const had = known.value.get(c.id)
  if (had && had.rev > c.rev) return
  known.value = new Map(known.value).set(c.id, c)
}

async function add(body: string): Promise<boolean> {
  const key = props.ticket.key
  const reply = await ops.run<CommentReply>((op) => api.addComment(key, { ...op, body }), { thing: `comment:new:${key}`, what: key })
  keep(reply?.comment)
  return !!reply
}

async function save(c: TicketComment, body: string): Promise<boolean> {
  if (body === c.body) {
    editing.value = ''
    return true
  }
  const key = props.ticket.key
  const reply = await ops.run<CommentReply>((op) => api.editComment(key, c.id, { ...op, body }), { thing: `comment:${c.id}`, what: `${key} #${c.n}`, expectedRev: c.rev })
  keep(reply?.comment)
  if (reply) editing.value = ''
  return !!reply
}

async function remove(c: TicketComment) {
  confirming.value = ''
  const key = props.ticket.key
  const reply = await ops.run<CommentReply>((op) => api.deleteComment(key, c.id, op), { thing: `comment:${c.id}`, what: `${key} #${c.n}`, expectedRev: c.rev })
  keep(reply?.comment)
}

const composer = useTemplateRef<{ insert: (text: string) => void }>('composer')
defineExpose({ insert: (text: string) => composer.value?.insert(text) })
</script>

<template>
  <section class="flex min-w-0 flex-col gap-3" data-ticket-comments :data-count="comments.filter((c) => !c.deletedAt).length">
    <h3 class="text-xs font-semibold uppercase tracking-wide text-muted">Comments</h3>

    <UButton v-if="before" :label="`Earlier comments (${before.after + before.limit} more)`" icon="i-lucide-chevrons-up" color="neutral" variant="soft" size="xs" class="w-fit" :loading="loadingEarlier" data-comments-earlier @click="more" />
    <p v-if="!comments.length" class="text-sm text-dimmed" data-comments-none>No comments yet.</p>

    <ol class="flex flex-col gap-3">
      <li v-for="c in comments" :key="c.id" class="flex min-w-0 gap-3" :data-comment="c.n" :data-comment-id="c.id" :data-comment-deleted="c.deletedAt ? '' : undefined">
        <span class="mt-0.5 grid size-7 flex-none place-items-center rounded-full text-[10px] font-semibold" :class="c.deletedAt ? 'bg-elevated text-dimmed' : avatarTone(commentAuthor(c))">{{ initials(commentAuthor(c)) }}</span>
        <div class="flex min-w-0 flex-1 flex-col gap-1">
          <p class="flex flex-wrap items-baseline gap-x-2 text-xs text-muted">
            <span class="font-medium text-highlighted" data-comment-author>{{ commentAuthor(c) }}</span>
            <span class="font-mono text-dimmed" data-comment-n>#{{ c.n }}</span>
            <span :title="c.createdAt">{{ relativeTime(c.createdAt, now, { suffix: true }) }}</span>
            <span v-if="c.editedAt && !c.deletedAt" :title="c.editedAt" data-comment-edited>· edited</span>
            <span v-if="!locked && !c.deletedAt && editing !== c.id" class="ml-auto flex gap-0.5">
              <UButton icon="i-lucide-pencil" size="xs" color="neutral" variant="ghost" :aria-label="`Edit comment #${c.n}`" data-comment-edit @click="editing = c.id" />
              <UButton icon="i-lucide-trash-2" size="xs" color="neutral" variant="ghost" :aria-label="`Delete comment #${c.n}`" data-comment-delete @click="confirming = c.id" />
            </span>
          </p>
          <p v-if="c.deletedAt" class="text-sm italic text-dimmed">This comment was deleted.</p>
          <DispatchCommentEditor v-else-if="editing === c.id" :initial="c.body" action="Save" cancellable :submit="(body) => save(c, body)" :attachment-url="attachmentUrl" @cancel="editing = ''" />
          <div v-else class="min-w-0 rounded-md ring ring-default" data-comment-body>
            <MarkdownPreview :text="c.body" dir="" :relative-links="false" :attachment-url="attachmentUrl" />
          </div>
          <div v-if="confirming === c.id" class="flex flex-wrap items-center gap-2 text-sm" data-comment-confirm>
            <span class="text-muted">Delete comment #{{ c.n }}? Its number stays, and the history keeps what it said.</span>
            <UButton label="Delete" color="error" variant="soft" size="xs" data-comment-delete-confirm @click="remove(c)" />
            <UButton label="Keep it" color="neutral" variant="ghost" size="xs" @click="confirming = ''" />
          </div>
        </div>
      </li>
    </ol>

    <DispatchCommentEditor v-if="!locked" ref="composer" action="Comment" :submit="add" :attachment-url="attachmentUrl" />
  </section>
</template>
