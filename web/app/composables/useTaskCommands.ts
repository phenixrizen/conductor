import type { RunTask } from '~/composables/useSessions'
import { CommandRefused, sendCommand, type TaskCommand } from '~/utils/tasks'

/**
 * The commands a person sends a managed run's task (#334, row 7): retry,
 * accept and cancel, each with a receipt (utils/tasks.ts), its outcome a
 * toast. `busy` names the task and command under way (`build:retry`), for
 * the button that sent it.
 */
export function useTaskCommands(runId: () => string) {
  const api = useSessions()
  const toast = useToast()
  const busy = ref('')

  const done: Record<TaskCommand, (id: string) => string> = {
    retry: (id) => `${id}: one more attempt allowed`,
    accept: (id) => `${id}: the result accepted`,
    cancel: (id) => `${id} cancelled`,
  }
  const icons: Record<TaskCommand, string> = { retry: 'i-lucide-rotate-ccw', accept: 'i-lucide-check', cancel: 'i-lucide-x' }

  async function send(t: Pick<RunTask, 'id'>, command: TaskCommand): Promise<boolean> {
    busy.value = `${t.id}:${command}`
    try {
      const r = await sendCommand((body) => api.taskCommand(runId(), t.id, command, body))
      toast.add({ title: done[command](t.id), description: r.repeat ? 'It was done already.' : undefined, icon: icons[command], color: 'success' })
      return true
    } catch (e) {
      toast.add({ title: `${t.id}: not ${command === 'cancel' ? 'cancelled' : command === 'retry' ? 'retried' : 'accepted'}`, description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: e instanceof CommandRefused ? 'warning' : 'error' })
      return false
    } finally {
      busy.value = ''
    }
  }

  /** What Cancel and Accept anyway ask before they run. */
  function question(t: Pick<RunTask, 'id' | 'state' | 'attempts'>, command: 'cancel' | 'accept'): { title: string; detail: string } {
    if (command === 'cancel') {
      return { title: `Cancel the task ${t.id}?`, detail: t.state === 'running' || t.state === 'verifying' ? 'Its attempt ends and its session is stopped; the tasks that need it never start. A retry can allow it one more attempt later.' : 'The tasks that need it never start. A retry can allow it one more attempt later.' }
    }
    return { title: `Accept ${t.id}'s last result anyway?`, detail: `Its last submitted commit becomes the task's result although a check failed it, and the tasks that need ${t.id} start from it.` }
  }

  return { busy, send, question }
}
