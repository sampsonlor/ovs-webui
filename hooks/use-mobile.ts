import * as React from 'react';

const MOBILE_BREAKPOINT = 768;
const mobileQuery = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`;

function subscribe(onChange: () => void) {
  const query = window.matchMedia(mobileQuery);
  query.addEventListener('change', onChange);
  return () => query.removeEventListener('change', onChange);
}

const getSnapshot = () => window.matchMedia(mobileQuery).matches;
const getServerSnapshot = () => false;

export function useIsMobile() {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
