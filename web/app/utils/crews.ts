import type { CrewInput } from '~/composables/useSessions'

/**
 * The body of POST /api/crews and PUT /api/crews/{id}: the fields the server accepts and nothing else, since it rejects unknown fields. A crew as
 * listed (a CrewInfo, with id, createdAt and updatedAt) or members carrying page state can be passed as they are.
 */
export function toCrewInput(c: CrewInput): CrewInput {
  return {
    name: c.name,
    goal: c.goal,
    cwd: c.cwd,
    where: c.where,
    isolation: c.isolation,
    openAfterLaunch: c.openAfterLaunch,
    viewLinkTtlSeconds: c.viewLinkTtlSeconds,
    members: c.members.map((m) => ({
      name: m.name,
      agentId: m.agentId,
      prompt: m.prompt,
      args: m.args,
      start: { when: m.start.when, member: m.start.member },
    })),
  }
}
