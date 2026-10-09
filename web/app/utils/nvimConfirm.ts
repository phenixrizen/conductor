/**
 * Neovim's own questions under the editor's Neovim keymap (round 13, G3):
 * a message of kind `confirm` (`:confirm q` with changes not written, a
 * write over a file changed outside) ends with its choices, the key of each
 * in brackets, the default's in square ones: "[Y]es, (N)o, (C)ancel: ".
 * The editor shows them as buttons that send the key. Pure.
 */
export interface ConfirmChoice {
  label: string
  key: string
  default: boolean
}

/** The choices of a confirm message's last line, none when it has no keys. */
export function confirmChoices(text: string): ConfirmChoice[] {
  const line = text.trim().split('\n').pop() ?? ''
  const out: ConfirmChoice[] = []
  for (const part of line.replace(/:\s*$/, '').split(',')) {
    const m = /^\s*([^[(]*)([[(])([^\])])[\])](.*?)\s*$/.exec(part)
    if (!m) continue
    out.push({ label: `${m[1]}${m[3]}${m[4]}`.trim(), key: m[3]!.toLowerCase(), default: m[2] === '[' })
  }
  return out
}

/** The question: the message without its line of choices. */
export function confirmQuestion(text: string): string {
  const lines = text.trim().split('\n')
  return (lines.length > 1 ? lines.slice(0, -1) : lines).join(' ').trim()
}
