import { useLayoutEffect, useRef } from 'react';

// Inactive workflows keep captured retry keys, but cannot continue requests or
// restore late responses. Scope/session changes still unmount the whole workbench.
export function useWorkflowActivity(visible: boolean, suspend: () => void) {
  const active = useRef(visible);
  const previous = useRef(visible);
  const callback = useRef(suspend);
  active.current = visible;
  callback.current = suspend;
  useLayoutEffect(() => {
    if (previous.current && !visible) callback.current();
    previous.current = visible;
  }, [visible]);
  return active;
}
