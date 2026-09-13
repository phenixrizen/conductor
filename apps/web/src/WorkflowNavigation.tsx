import { useRef } from 'react';

export const workflows = [
  { id: 'review', label: 'Review', description: 'Inspect shared work packages, compare revisions and approve the exact design.' },
  { id: 'source', label: 'Source & graph', description: 'Collect repository context and inspect relationships across shared source.' },
  { id: 'agents', label: 'Agent work', description: 'Review coordinated tasks, authorize execution and inspect retained reports.' },
  { id: 'delivery', label: 'Delivery', description: 'Inspect verified patches and authorize draft pull or merge requests.' },
  { id: 'tracker', label: 'Tracker', description: 'Connect work to the workspace’s Linear or Jira tickets and inspect synchronization.' },
  { id: 'runtime', label: 'Runtime', description: 'Compare deployment observations with the approved criteria for an exact time window.' },
] as const;
export type Workflow = typeof workflows[number]['id'];

export function WorkflowNavigation({ selected, onSelect }: { selected: Workflow; onSelect: (value: Workflow) => void }) {
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  return <nav className="workflow-navigation" aria-label="Workbench workflows">
    <div role="tablist" aria-label="Engineering workflows" onKeyDown={event => {
      const index = workflows.findIndex(value => value.id === selected);
      const next = event.key === 'ArrowRight' ? (index + 1) % workflows.length
        : event.key === 'ArrowLeft' ? (index + workflows.length - 1) % workflows.length
        : event.key === 'Home' ? 0 : event.key === 'End' ? workflows.length - 1 : -1;
      if (next < 0) return;
      event.preventDefault();
      onSelect(workflows[next].id);
      buttons.current[next]?.focus();
      buttons.current[next]?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }}>
      {workflows.map((value, index) => <button key={value.id} ref={button => { buttons.current[index] = button; }}
        type="button" role="tab" id={`workflow-tab-${value.id}`} aria-controls={`workflow-${value.id}`}
        aria-selected={selected === value.id} tabIndex={selected === value.id ? 0 : -1}
        onClick={() => onSelect(value.id)}>{value.label}</button>)}
    </div>
    <a className="workflow-skip" href={`#workflow-${selected}`}>Go to active workflow</a>
  </nav>;
}
