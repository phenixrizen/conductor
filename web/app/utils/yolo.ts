import type { AgentInfo, SessionInfo } from '~/composables/useSessions'

/** A crew's yolo choice as its editor's select shows it: the server's default, or on, or off. */
export type YoloChoice = 'default' | 'on' | 'off'

/** The select's value for a crew's `yolo`. */
export function yoloChoice(yolo: boolean | undefined): YoloChoice {
  return yolo === undefined ? 'default' : yolo ? 'on' : 'off'
}

/** A crew's `yolo` for the select's value: left out for the server's default. */
export function yoloFromChoice(choice: YoloChoice): boolean | undefined {
  return choice === 'default' ? undefined : choice === 'on'
}

/** Whether a launch runs with yolo: its own choice, else the server's default. */
export function effectiveYolo(choice: boolean | undefined, serverDefault: boolean): boolean {
  return choice ?? serverDefault
}

/** Whether an agent has a yolo recipe: something to add to a launch. */
export function hasYoloRecipe(agent: Pick<AgentInfo, 'yolo'> | undefined): boolean {
  return !!(agent?.yolo?.args?.length || Object.keys(agent?.yolo?.env ?? {}).length)
}

/**
 * What a launch of `agent` with `extra` arguments runs, yolo `on` or not: the argv the server builds (its command, the arguments, the
 * recipe's arguments when it applies; the adapter's flags come after, on the server), the recipe's variables by name, and a notice when
 * yolo is on and the agent has no recipe: it is launched as it would be without, and no badge shows.
 */
export function yoloSummary(agent: Pick<AgentInfo, 'name' | 'command' | 'yolo'> | undefined, on: boolean, extra: string[] = []): { argv: string[]; env: string[]; applies: boolean; notice: string } {
  if (!agent) return { argv: [], env: [], applies: false, notice: '' }
  const applies = on && hasYoloRecipe(agent)
  return {
    argv: [...agent.command, ...extra, ...(applies ? (agent.yolo?.args ?? []) : [])],
    env: applies ? Object.keys(agent.yolo?.env ?? {}).sort() : [],
    applies,
    notice: on && !applies ? `${agent.name} has no yolo recipe: it launches as usual, with its permission prompts.` : '',
  }
}

/**
 * What an ended session's start-again button says: Resume when its agent has a conversation to resume (a session recipe, and a turn had),
 * else Relaunch, which starts a new conversation. The server decides in the end, and says why when it relaunches.
 */
export function resumeLabel(session: Pick<SessionInfo, 'agentSession'> | undefined): { label: 'Resume' | 'Relaunch'; icon: string; tooltip: string } {
  if (session?.agentSession?.resumable) return { label: 'Resume', icon: 'i-lucide-history', tooltip: 'Starts it again with its conversation' }
  return { label: 'Relaunch', icon: 'i-lucide-rotate-ccw', tooltip: 'Starts it again: a new conversation' }
}
