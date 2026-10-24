/**
 * The lines this browser typed as answers to questions, by the question's id (#364). The owner never repeats a typed line in the
 * chat (the agent may have asked for a credential, the terminal may not echo it, and the chat goes to every viewer), so the
 * person who typed it sees it under the question from here alone, for as long as the page lives.
 */
export function useTypedAnswers() {
  const typed = useState<Record<string, string>>('typedAnswers', () => ({}))
  /** remember keeps text as this browser's answer to the question, once the owner took it. */
  function remember(question: string | undefined, text: string) {
    if (!question || !text) return
    typed.value = { ...typed.value, [question]: text }
  }
  /** of is the line this browser typed for the question, '' when it typed none. */
  function of(question: string): string {
    return typed.value[question] ?? ''
  }
  return { remember, of }
}
