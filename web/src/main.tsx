import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { FRESH_PAGE } from './screens/allSessions';
import { App, takeStartupLinks } from './screens/App';
import { ErrorBoundary, ErrorScreen, errorText } from './screens/chunks';
import './styles/global.css';

// A reload past the service worker's cached page (FRESH_PAGE) is done: drop its flag from the address.
const address = new URL(window.location.href);
if (address.searchParams.has(FRESH_PAGE)) {
  address.searchParams.delete(FRESH_PAGE);
  window.history.replaceState(window.history.state, '', `${address.pathname}${address.search}${address.hash}`);
}

// Read and strip every URL fragment before anything renders. Grants live
// only in memory until they are redeemed.
const links = takeStartupLinks(window.location, window.history, window.location);

const root = document.getElementById('root');

if (root === null) {
  throw new Error('Missing application root');
}

createRoot(root).render(
  <StrictMode>
    <ErrorBoundary fallback={(error) => <ErrorScreen text={errorText(error)} />}>
      <App {...links} />
    </ErrorBoundary>
  </StrictMode>,
);
